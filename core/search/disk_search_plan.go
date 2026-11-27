package search

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/anaregdesign/tarp"
)

type diskClause struct {
	class TokenClass
	terms []*diskQueryTerm
}

type diskPlan struct {
	clauses    []diskClause
	exact      []*diskQueryTerm // ordered, including repeated terms, for phrase/boost
	threshold  int
	phrase     bool
	impossible bool
}

func (q *diskQuery) term(token Token) (*diskQueryTerm, error) {
	if term, known := q.terms[token]; known {
		return term, nil
	}
	key := diskKey(q.index.base, diskTermRow, uint8(token.Class), token.Term)
	value, err := q.read(key)
	var meta diskTermValue
	if !errors.Is(err, tarp.ErrNotFound) {
		if err != nil {
			return nil, err
		}
		meta, err = decodeDiskTerm(value)
		if err != nil {
			return nil, err
		}
	}
	if meta.documents > q.index.footer.Documents {
		return nil, diskInvalid("term references too many documents")
	}
	for field, df := range meta.df {
		if uint64(df) > q.index.footer.Stats[token.Class][field].Count {
			return nil, diskInvalid("document frequency exceeds corpus count")
		}
	}
	df, live, err := q.liveDF(token, meta)
	if err != nil {
		return nil, err
	}
	if !live {
		q.terms[token] = nil
		return nil, nil
	}
	term := &diskQueryTerm{token: token, meta: meta, liveDF: df}
	q.terms[token] = term
	return term, nil
}

func (q *diskQuery) plan(tokens []Token, opts MatchOptions, phrase bool) (diskPlan, error) {
	plan := diskPlan{phrase: phrase}
	var words int
	phraseTerms := make(map[Token]bool, len(tokens))
	for _, token := range tokens {
		if int(token.Class) >= numTokenClasses {
			continue
		}
		term, err := q.term(token)
		if err != nil {
			return diskPlan{}, err
		}
		if term != nil && token.Class == ClassWord {
			plan.exact = append(plan.exact, term)
		}
		clause := diskClause{class: token.Class}
		if term != nil {
			clause.terms = append(clause.terms, term)
		}
		if phrase {
			if term == nil || !q.index.config.positions {
				return diskPlan{impossible: true}, nil
			}
			if phraseTerms[token] {
				continue
			}
			phraseTerms[token] = true
		} else if token.Class == ClassWord && opts.expandsTerms() && isExpandable(token.Term) {
			additional, err := q.expand(token, opts, MaxTermExpansions-len(clause.terms))
			if err != nil {
				return diskPlan{}, err
			}
			clause.terms = append(clause.terms, additional...)
		}
		if token.Class == ClassWord {
			words++
		}
		plan.clauses = append(plan.clauses, clause)
	}
	if phrase {
		plan.threshold = words
	} else {
		plan.threshold = coverageThreshold(opts, words)
	}
	return plan, nil
}

func (q *diskQuery) expand(token Token, opts MatchOptions, limit int) ([]*diskQueryTerm, error) {
	prefix := diskKey(q.index.base, diskTermRow, uint8(token.Class))
	lowerPrefix := prefix
	if opts.Fuzziness <= 0 {
		lowerPrefix = append(bytes.Clone(prefix), tarp.EncodeOrderedStringPrefix(token.Term)...)
	}
	var best expansionTopHeap
	consider := q.expansionConsiderer(token.Term, opts, limit, &best)
	seen := make(map[Token]struct{})
	for row, readErr := range diskRange(q.work.ctx, q.snap, lowerPrefix) {
		if readErr != nil {
			return nil, readErr
		}
		if err := q.work.visit(WorkDiskReadBytes, int64(len(row.Key)+len(row.Value))); err != nil {
			return nil, err
		}
		if err := q.work.visit(WorkDictionaryVisits, 1); err != nil {
			return nil, err
		}
		candidate, err := (tarp.OrderedStringCodec{}).Decode(row.Key[len(prefix):])
		if err != nil || candidate == "" {
			return nil, diskInvalid("malformed dictionary key")
		}
		expanded := Token{Class: token.Class, Term: candidate}
		if q.overlay.terms[expanded] != nil {
			seen[expanded] = struct{}{}
		}
		if _, expired := q.overlay.removedDF[expanded]; expired {
			meta, err := decodeDiskTerm(row.Value)
			if err != nil {
				return nil, err
			}
			_, live, err := q.liveDF(expanded, meta)
			if err != nil {
				return nil, err
			}
			if !live {
				continue
			}
		}
		if err := consider(candidate); err != nil {
			return nil, err
		}
	}
	for expanded := range q.overlay.terms {
		if expanded.Class != token.Class {
			continue
		}
		if _, already := seen[expanded]; already {
			continue
		}
		if err := q.work.visit(WorkDictionaryVisits, 1); err != nil {
			return nil, err
		}
		if err := consider(expanded.Term); err != nil {
			return nil, err
		}
	}
	sort.Slice(best, func(i, j int) bool { return betterExpansion(best[i], best[j]) })
	if err := q.work.visit(WorkExpansionRetained, int64(len(best))); err != nil {
		return nil, err
	}
	out := make([]*diskQueryTerm, 0, len(best))
	for _, candidate := range best {
		term, err := q.term(Token{Class: token.Class, Term: candidate.term})
		if err != nil {
			return nil, err
		}
		if term == nil {
			return nil, diskInvalid("dictionary entry disappeared in snapshot")
		}
		out = append(out, term)
	}
	return out, nil
}

func (q *diskQuery) expansionConsiderer(term string, opts MatchOptions, limit int, best *expansionTopHeap) func(string) error {
	var queryRunes []rune
	var first, second []int
	fuzziness := min(opts.Fuzziness, 2)
	if fuzziness > 0 {
		queryRunes = []rune(term)
		first = make([]int, len(queryRunes)+fuzziness+1)
		second = make([]int, len(first))
	}
	queryRunesCount := utf8.RuneCountInString(term)
	return func(candidate string) error {
		if candidate == term {
			return nil
		}
		if opts.PrefixTerms && strings.HasPrefix(candidate, term) {
			retainExpansion(best, expansionCandidate{term: candidate, kind: expansionPrefix, quality: utf8.RuneCountInString(candidate) - queryRunesCount}, limit)
			return nil
		}
		if fuzziness <= 0 {
			return nil
		}
		length := utf8.RuneCountInString(candidate)
		if length < len(queryRunes)-fuzziness || length > len(queryRunes)+fuzziness {
			return nil
		}
		distance, matched, err := editDistanceWithinRowsTracked(queryRunes, []rune(candidate), fuzziness, first, second, q.work)
		if err != nil {
			return err
		}
		if matched {
			retainExpansion(best, expansionCandidate{term: candidate, kind: expansionFuzzy, quality: distance}, limit)
		}
		return nil
	}
}
