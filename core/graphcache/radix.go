package graphcache

import (
	"strings"
	"sync"
)

// radix is a minimal, concurrency-safe Patricia (compressed) trie over
// byte strings. It is the secondary index that powers prefix-keyed
// queries on GraphCache (ScanByPrefix, CountByPrefix, DeleteByPrefix).
//
// Design notes:
//
//   - Keys are byte strings, not S. The owning GraphCache projects S to
//     string via a caller-supplied extractor (see EnablePrefixIndex) so
//     the index works for any S whose natural identity is textual without
//     coupling the cache type parameter to ~string.
//
//   - Patricia (path-compressed) rather than a flat trie: typical Lantern
//     keys are namespaced (e.g. "session:abc123:msg:42") where the
//     non-branching portions dominate, so compression gives a substantial
//     memory win over the naive one-node-per-byte layout.
//
//   - Children are stored in a sorted slice keyed by the first byte of
//     each child's edge label. Sorted slices beat maps for the typical
//     low branching factor (often <16) seen on namespaced keys, and they
//     give us predictable iteration order — which Phase 2 will need when
//     it formalises the cursor encoding.
//
//   - Concurrency: radix carries its own sync.RWMutex. The GraphCache
//     callers always hold c.mu when mutating, but scan paths only hold
//     c.mu.RLock(); the inner RWMutex on radix lets concurrent scans
//     proceed without serialising on a single goroutine. The lock order
//     established by GraphCache is c.mu → {dict.mu, radix.mu}; radix
//     never calls back into GraphCache, so no cycle is possible.
//
//   - Size tracking is maintained incrementally on insert/delete so Len
//     is O(1). CountByPrefix walks the matching subtree (it is bounded
//     by the result-set size, not the total tree size).
type radix struct {
	mu   sync.RWMutex
	root *radixNode
	size int
}

// radixNode is one node in the Patricia trie. The root has an empty label;
// every other node stores the byte slice that distinguishes it from its
// parent. terminal marks whether the path from the root to this node
// represents a stored key (vs. an internal branching point only).
//
// children are sorted by children[i].label[0]; we keep them in a slice so
// the binary-search lookup is cache-friendly and so iteration is
// deterministic without an extra sort step.
type radixNode struct {
	label    string
	terminal bool
	children []*radixNode
}

// newRadix returns an empty trie. The root node is allocated eagerly so
// every other code path can assume root is non-nil.
func newRadix() *radix {
	return &radix{root: &radixNode{}}
}

// len returns the number of stored keys. O(1).
func (r *radix) len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.size
}

// insert adds key to the trie. It returns true iff the key was not
// already present (so the caller can keep an external count in sync
// without a second lookup).
func (r *radix) insert(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.insertAt(r.root, key) {
		r.size++
		return true
	}
	return false
}

// insertAt is the recursive workhorse for insert. It returns true iff a
// new terminal was created.
func (r *radix) insertAt(n *radixNode, key string) bool {
	// Empty key at this node: mark this node terminal.
	if key == "" {
		if n.terminal {
			return false
		}
		n.terminal = true
		return true
	}
	idx, child := n.findChild(key[0])
	if child == nil {
		// No matching child — append a new leaf carrying the whole
		// remaining key as its edge label.
		n.insertChildAt(idx, &radixNode{label: key, terminal: true})
		return true
	}
	common := commonPrefixLen(child.label, key)
	if common == len(child.label) {
		// Child's whole label is a prefix of the remaining key — descend.
		return r.insertAt(child, key[common:])
	}
	// Need to split the child: introduce an intermediate node that holds
	// the shared prefix, push the existing child's tail beneath it, and
	// attach the new key (or mark the intermediate terminal if the new
	// key ends exactly at the split point).
	tail := &radixNode{
		label:    child.label[common:],
		terminal: child.terminal,
		children: child.children,
	}
	split := &radixNode{
		label:    child.label[:common],
		children: []*radixNode{tail},
	}
	n.children[idx] = split
	if common == len(key) {
		split.terminal = true
	} else {
		leaf := &radixNode{label: key[common:], terminal: true}
		split.insertChildSorted(leaf)
	}
	return true
}

// delete removes key. It returns true iff the key was present.
//
// Internal nodes left without a terminal and with a single child are
// collapsed back into their parent's edge so the tree stays Patricia-
// shaped after arbitrary delete sequences (otherwise repeated insert /
// delete cycles would degrade prefix-walk performance).
func (r *radix) delete(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.deleteAt(r.root, key) {
		r.size--
		return true
	}
	return false
}

// deleteMany removes every supplied key under a single write-lock
// acquisition, decrementing size once per key actually removed, and returns
// the number removed. Keys absent from the radix are skipped. It is the batch
// sibling of delete used by GraphCache's batch eviction path so a large delete
// pays one radix.mu acquisition instead of one per key (#738).
func (r *radix) deleteMany(keys []string) int {
	if len(keys) == 0 {
		return 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var n int
	for _, key := range keys {
		if r.deleteAt(r.root, key) {
			r.size--
			n++
		}
	}
	return n
}

func (r *radix) deleteAt(n *radixNode, key string) bool {
	if key == "" {
		if !n.terminal {
			return false
		}
		n.terminal = false
		return true
	}
	idx, child := n.findChild(key[0])
	if child == nil {
		return false
	}
	if len(key) < len(child.label) || key[:len(child.label)] != child.label {
		return false
	}
	if !r.deleteAt(child, key[len(child.label):]) {
		return false
	}
	// Post-deletion compaction. The child might be:
	//   (a) a leaf with no remaining terminal → drop it entirely;
	//   (b) an internal node with exactly one child and no terminal →
	//       merge it with that child by concatenating the labels.
	if !child.terminal && len(child.children) == 0 {
		n.children = append(n.children[:idx], n.children[idx+1:]...)
		return true
	}
	if !child.terminal && len(child.children) == 1 {
		only := child.children[0]
		only.label = child.label + only.label
		n.children[idx] = only
	}
	return true
}

// walkPrefix invokes fn for every stored key that has prefix as a
// prefix, in lexicographic order. fn may return false to stop the walk
// early. walkPrefix holds the read lock for the whole traversal, so fn
// must not call back into a write path on the same radix.
func (r *radix) walkPrefix(prefix string, fn func(key string) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	node, accumulated, ok := r.descend(prefix)
	if !ok {
		return
	}
	buf := append(make([]byte, 0, len(accumulated)+16), accumulated...)
	walkAll(node, &buf, fn)
}

// walkPrefixBound is the seek-aware sibling of walkPrefix (#836): it visits,
// in lexicographic order, every stored key that has prefix as a prefix AND
// sits past bound — strictly greater when inclusive is false (the resume-
// after-cursor case), or >= bound when inclusive is true (the resume-AT case
// the edge scan needs for its partially-consumed tail). Children are sorted,
// so whole subtrees on the wrong side of bound are skipped without being
// visited — a resumed page costs O(depth + page), not O(everything before
// the cursor). A bound lexicographically below the prefix subtree degrades
// to a plain walkPrefix; an empty bound with inclusive=false is exactly
// walkPrefix.
func (r *radix) walkPrefixBound(prefix, bound string, inclusive bool, fn func(key string) bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	node, accumulated, ok := r.descend(prefix)
	if !ok {
		return
	}
	buf := append(make([]byte, 0, len(accumulated)+16), accumulated...)
	walkBound(node, &buf, bound, inclusive, fn)
}

// walkBound visits the terminals under n (whose accumulated path is *buf)
// that satisfy the bound, pruning subtrees that provably cannot. The three
// path-vs-bound relationships drive it:
//
//   - path > bound: every key below starts with path, so all qualify —
//     degrade to the plain walkAll.
//   - path is not a prefix of bound (and path <= bound): every key below
//     shares path's divergence point with bound, so all compare < bound —
//     skip the whole subtree.
//   - path is a (possibly equal) prefix of bound: the boundary chain. The
//     terminal at exactly bound is emitted only when inclusive; children are
//     binary-searched so subtrees entirely below the bound's next byte are
//     skipped, the one straddling it recurses, and everything after it walks
//     freely.
func walkBound(n *radixNode, buf *[]byte, bound string, inclusive bool, fn func(string) bool) bool {
	path := string(*buf)
	if path > bound {
		return walkAll(n, buf, fn)
	}
	if !strings.HasPrefix(bound, path) {
		// path <= bound and diverges from it: everything below is < bound.
		return true
	}
	rest := bound[len(path):]
	if rest == "" {
		// path == bound: emit the exact-match terminal only when inclusive;
		// every child subtree is strictly greater and walks freely.
		if n.terminal && inclusive {
			if !fn(path) {
				return false
			}
		}
		for _, c := range n.children {
			*buf = append(*buf, c.label...)
			ok := walkAll(c, buf, fn)
			*buf = (*buf)[:len(*buf)-len(c.label)]
			if !ok {
				return false
			}
		}
		return true
	}
	// path is a proper prefix of bound: the terminal here is < bound (skip).
	// Find the first child that could reach the bound: children with a first
	// byte below rest[0] are entirely < bound.
	idx, exact := n.findChild(rest[0])
	if exact == nil {
		// No child straddles the boundary byte; children from idx on are
		// strictly greater.
	} else {
		c := exact
		m := len(c.label)
		if len(rest) < m {
			m = len(rest)
		}
		switch {
		case c.label[:m] < rest[:m]:
			// Entire child subtree < bound (diverges below) — skip it and
			// continue with the strictly-greater children after it.
			idx++
		case c.label[:m] > rest[:m]:
			// Entire child subtree > bound (diverges above while sharing the
			// boundary byte) — walk it freely here; the tail loop below skips
			// the boundary byte and would otherwise drop it.
			*buf = append(*buf, c.label...)
			ok := walkAll(c, buf, fn)
			*buf = (*buf)[:len(*buf)-len(c.label)]
			if !ok {
				return false
			}
			idx++
		default:
			// Shared prefix for the full overlap: the child either still
			// tracks the bound (label consumed by rest → recurse) or has
			// already overrun it (label longer than rest → path+label starts
			// with bound and is longer, i.e. strictly greater → walk freely;
			// walkBound's path>bound arm handles that uniformly).
			*buf = append(*buf, c.label...)
			ok := walkBound(c, buf, bound, inclusive, fn)
			*buf = (*buf)[:len(*buf)-len(c.label)]
			if !ok {
				return false
			}
			idx++
		}
	}
	for _, c := range n.children[idx:] {
		if c.label[0] < rest[0] {
			continue
		}
		if c.label[0] == rest[0] {
			// Only reachable when exact was handled above; skip defensively.
			continue
		}
		*buf = append(*buf, c.label...)
		ok := walkAll(c, buf, fn)
		*buf = (*buf)[:len(*buf)-len(c.label)]
		if !ok {
			return false
		}
	}
	return true
}

// countPrefix returns the number of stored keys with prefix as a prefix.
// It walks the matching subtree; if prefix is empty this is O(size).
// A subtree-size cache would make this O(k) but is deferred until a
// benchmark proves it matters.
func (r *radix) countPrefix(prefix string) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	node, _, ok := r.descend(prefix)
	if !ok {
		return 0
	}
	n := 0
	countAll(node, &n)
	return n
}

// descend navigates from the root down to the deepest node whose
// accumulated label is a prefix of `prefix`. It returns that node, the
// label accumulated to reach it, and ok=true iff every byte of `prefix`
// was consumed (either by descending past it or by stopping at a node
// whose remaining edge label still starts with the rest of `prefix`).
//
// The trailing-partial case matters: walkPrefix("ab") must still visit
// every key under an edge labelled "abcdef". descend returns the node
// at the end of that edge so the caller iterates its whole subtree.
func (r *radix) descend(prefix string) (*radixNode, string, bool) {
	n := r.root
	consumed := ""
	for {
		if prefix == "" {
			return n, consumed, true
		}
		_, child := n.findChild(prefix[0])
		if child == nil {
			return nil, "", false
		}
		if len(prefix) < len(child.label) {
			if child.label[:len(prefix)] != prefix {
				return nil, "", false
			}
			return child, consumed + child.label, true
		}
		if prefix[:len(child.label)] != child.label {
			return nil, "", false
		}
		consumed += child.label
		prefix = prefix[len(child.label):]
		n = child
	}
}

// walkAll visits every terminal in the subtree rooted at n. The accumulated
// path lives in a single shared byte buffer that is appended on descent and
// truncated on ascent (#836), so a walk allocates one string per EMITTED
// terminal instead of one per visited node edge. fn returning false aborts
// the walk; the boolean propagates up so iteration stops promptly.
func walkAll(n *radixNode, buf *[]byte, fn func(string) bool) bool {
	if n.terminal {
		if !fn(string(*buf)) {
			return false
		}
	}
	for _, c := range n.children {
		*buf = append(*buf, c.label...)
		ok := walkAll(c, buf, fn)
		*buf = (*buf)[:len(*buf)-len(c.label)]
		if !ok {
			return false
		}
	}
	return true
}

func countAll(n *radixNode, into *int) {
	if n.terminal {
		*into++
	}
	for _, c := range n.children {
		countAll(c, into)
	}
}

// findChild returns the index at which a child keyed by b lives (or
// would live, for ordered insertion) and the child itself if present.
// Binary search keeps this O(log B) for branching factor B; in practice
// B is small (<16 for namespaced keys) so even a linear scan would be
// fine, but binary search costs nothing extra.
func (n *radixNode) findChild(b byte) (int, *radixNode) {
	lo, hi := 0, len(n.children)
	for lo < hi {
		mid := (lo + hi) / 2
		c := n.children[mid].label[0]
		switch {
		case c == b:
			return mid, n.children[mid]
		case c < b:
			lo = mid + 1
		default:
			hi = mid
		}
	}
	return lo, nil
}

func (n *radixNode) insertChildAt(idx int, child *radixNode) {
	n.children = append(n.children, nil)
	copy(n.children[idx+1:], n.children[idx:])
	n.children[idx] = child
}

func (n *radixNode) insertChildSorted(child *radixNode) {
	idx, existing := n.findChild(child.label[0])
	if existing != nil {
		// Caller bug: insertChildSorted is only used during split, when
		// the splitting code has just allocated a fresh intermediate
		// node whose only child is the surviving tail. A second insert
		// at the same byte therefore cannot happen.
		panic("radix: insertChildSorted collision (caller bug)")
	}
	n.insertChildAt(idx, child)
}

func commonPrefixLen(a, b string) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return n
}
