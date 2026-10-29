package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/anaregdesign/lantern/server/benchheap"
)

// Host-only bind evidence does not alter the target Session/Completion protocol.
type retainedMountHost struct {
	Device string `json:"device"`
	Inode  string `json:"inode"`
	Type   string `json:"type"`
	UID    int    `json:"uid"`
	GID    int    `json:"gid"`
	Mode   int    `json:"mode"`
}
type retainedMountBinding struct {
	Schema             int                        `json:"schema"`
	Protocol           string                     `json:"protocol"`
	Context            map[string]json.RawMessage `json:"context"`
	Service            string                     `json:"service"`
	Session            benchheap.Session          `json:"session"`
	CanonicalDirectory string                     `json:"canonical_directory"`
	DaemonID           string                     `json:"daemon_id"`
	HostPrecreateFile  retainedHeapReference      `json:"host_precreate_file"`
	HostBeforeFile     retainedHeapReference      `json:"host_before_file"`
	HostAfterFile      retainedHeapReference      `json:"host_after_file"`
	OriginalBeforeFile retainedHeapReference      `json:"original_before_file"`
	OriginalAfterFile  retainedHeapReference      `json:"original_after_file"`
	DaemonBeforeFile   retainedHeapReference      `json:"daemon_before_file"`
	DaemonAfterFile    retainedHeapReference      `json:"daemon_after_file"`
	PairsFile          retainedHeapReference      `json:"pairs_file"`
	Probe              json.RawMessage            `json:"probe"`
	CloneBeforeFile    retainedHeapReference      `json:"clone_before_file"`
	CloneAfterFile     retainedHeapReference      `json:"clone_after_file"`
}
type retainedMountProbe struct {
	CreatedFile     retainedHeapReference `json:"created_file"`
	CompletedFile   retainedHeapReference `json:"completed_file"`
	OutputFile      retainedHeapReference `json:"output_file"`
	WaitFile        retainedHeapReference `json:"wait_file"`
	RemoveFile      retainedHeapReference `json:"remove_file"`
	EnvironmentFile retainedHeapReference `json:"environment_file"`
}
type retainedMountPair struct {
	Destination    string `json:"destination"`
	DeclaredSource string `json:"declared_source"`
	OriginalSource string `json:"original_source"`
	CloneSource    string `json:"clone_source"`
	ExpectedType   string `json:"expected_type"`
}
type retainedMountSnapshot struct {
	Schema       int                   `json:"schema"`
	Protocol     string                `json:"protocol"`
	Phase        string                `json:"phase"`
	Position     string                `json:"position"`
	Service      string                `json:"service"`
	HostIdentity retainedMountHost     `json:"host_identity"`
	InspectFile  retainedHeapReference `json:"inspect_file"`
	DaemonFile   retainedHeapReference `json:"daemon_file"`
}

func validateRetainedMount(root, phase string, index int, node retainedHeapNode, ctx retainedHeapContext) error {
	bad := errors.New("invalid retained mount binding/raw evidence")
	base := filepath.Join(root, "heap-mount-evidence", fmt.Sprintf("lantern-%d", index))
	seen := map[string]int{}
	raw := func(ref retainedHeapReference) ([]byte, error) {
		if filepath.Dir(ref.Path) != base || filepath.Clean(ref.Path) != ref.Path || !retainedHex(ref.SHA256, 64) {
			return nil, bad
		}
		physical, err := filepath.EvalSymlinks(ref.Path)
		if err != nil || physical != ref.Path {
			return nil, bad
		}
		data, err := readRetainedFile(ref.Path, 128<<10)
		if err != nil || retainedReference(ref.Path, data).SHA256 != ref.SHA256 {
			return nil, bad
		}
		seen[ref.Path] = len(data)
		total := 0
		for _, n := range seen {
			total += n
		}
		if len(seen) > 32 || total > 2<<20 {
			return nil, bad
		}
		return data, nil
	}
	decode := func(ref retainedHeapReference, value any) error {
		data, err := raw(ref)
		if err != nil {
			return err
		}
		return benchheap.Decode(data, value, 128<<10)
	}
	object := func(ref retainedHeapReference) (map[string]any, error) {
		data, err := raw(ref)
		if err != nil {
			return nil, err
		}
		var check map[string]json.RawMessage
		if benchheap.Decode(data, &check, 128<<10) != nil {
			return nil, bad
		}
		var out map[string]any
		if json.Unmarshal(data, &out) != nil {
			return nil, bad
		}
		return out, nil
	}
	if node.MountBindingFile.Path != filepath.Join(base, "binding.json") {
		return bad
	}
	var bind retainedMountBinding
	// Decode the nullable probe through RawMessage separately; other fields stay exact.
	data, err := raw(node.MountBindingFile)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if benchheap.Decode(data, &fields, 128<<10) != nil || len(fields) != 18 {
		return bad
	}
	for _, name := range []string{"schema", "protocol", "context", "service", "session", "canonical_directory", "daemon_id", "host_precreate_file", "host_before_file", "host_after_file", "original_before_file", "original_after_file", "daemon_before_file", "daemon_after_file", "pairs_file", "probe", "clone_before_file", "clone_after_file"} {
		if _, ok := fields[name]; !ok {
			return bad
		}
	}
	// RawMessage has byte-slice semantics, so decode the explicit outer object with
	// duplicate scanning above and strict nested records below.
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if dec.Decode(&bind) != nil {
		return bad
	}
	if benchheap.Decode(fields["session"], &bind.Session, 4096) != nil {
		return bad
	}
	for name, ref := range map[string]*retainedHeapReference{"host_precreate_file": &bind.HostPrecreateFile, "host_before_file": &bind.HostBeforeFile, "host_after_file": &bind.HostAfterFile, "original_before_file": &bind.OriginalBeforeFile, "original_after_file": &bind.OriginalAfterFile, "daemon_before_file": &bind.DaemonBeforeFile, "daemon_after_file": &bind.DaemonAfterFile, "pairs_file": &bind.PairsFile, "clone_before_file": &bind.CloneBeforeFile, "clone_after_file": &bind.CloneAfterFile} {
		if benchheap.Decode(fields[name], ref, 4096) != nil {
			return bad
		}
	}
	if bind.Schema != 1 || bind.Protocol != benchheap.Protocol || bind.Service != fmt.Sprintf("lantern-%d", index) || bind.Session != node.Completion.Request.Identity.Session || bind.CanonicalDirectory != filepath.Join(root, "heap-control-evidence", bind.Service) || bind.DaemonID == "" || len(bind.DaemonID) > 128 {
		return bad
	}
	for _, c := range bind.DaemonID {
		if c <= 32 || c >= 127 {
			return bad
		}
	}
	contextJSON, _ := json.Marshal(ctx)
	var contextMap map[string]json.RawMessage
	_ = json.Unmarshal(contextJSON, &contextMap)
	delete(contextMap, "workload")
	if len(bind.Context) != len(contextMap) {
		return bad
	}
	for k, v := range contextMap {
		var a, b any
		if json.Unmarshal(v, &a) != nil || json.Unmarshal(bind.Context[k], &b) != nil || !reflect.DeepEqual(a, b) {
			return bad
		}
	}
	owner := strings.Split(ctx.RuntimeUser, ":")
	if len(owner) != 2 {
		return bad
	}
	uid, e := strconv.Atoi(owner[0])
	if e != nil {
		return bad
	}
	gid, e := strconv.Atoi(owner[1])
	if e != nil {
		return bad
	}
	hostOK := func(h retainedMountHost) bool {
		positive := func(s string) bool {
			if s == "" || s[0] == '0' {
				return false
			}
			for _, c := range s {
				if c < '0' || c > '9' {
					return false
				}
			}
			return true
		}
		return positive(h.Device) && positive(h.Inode) && h.Type == "directory" && h.UID == uid && h.GID == gid && h.Mode == 448
	}
	for ref, name := range map[*retainedHeapReference]string{&bind.HostPrecreateFile: "host-precreate.json", &bind.HostBeforeFile: "host-before.json", &bind.HostAfterFile: "host-after.json", &bind.OriginalBeforeFile: "original-before.json", &bind.OriginalAfterFile: "original-after.json", &bind.DaemonBeforeFile: "daemon-before", &bind.DaemonAfterFile: "daemon-after", &bind.PairsFile: "requested-pairs.json", &bind.CloneBeforeFile: "clone-before.json", &bind.CloneAfterFile: "clone-after.json"} {
		if ref.Path != filepath.Join(base, name) {
			return bad
		}
	}
	var identity retainedMountHost
	if decode(bind.HostPrecreateFile, &identity) != nil || !hostOK(identity) {
		return bad
	}
	for _, ref := range []retainedHeapReference{bind.HostBeforeFile, bind.HostAfterFile} {
		var h retainedMountHost
		if decode(ref, &h) != nil || h != identity {
			return bad
		}
	}
	daemon := bind.DaemonID + "\n"
	for _, ref := range []retainedHeapReference{bind.DaemonBeforeFile, bind.DaemonAfterFile} {
		b, e := raw(ref)
		if e != nil || string(b) != daemon {
			return bad
		}
	}
	m := func(v any) map[string]any { x, _ := v.(map[string]any); return x }
	arr := func(v any) []any { x, _ := v.([]any); return x }
	eq := reflect.DeepEqual
	serving := func(ref retainedHeapReference) (map[string]any, error) {
		o, e := object(ref)
		if e != nil {
			return nil, e
		}
		keys := []string{"schema", "kind", "id", "name", "image", "user", "project", "service", "oneoff", "owner", "initializer", "entrypoint", "cmd", "working_dir", "host_config", "mounts", "state", "restart_count"}
		if len(o) != len(keys) {
			return nil, bad
		}
		for _, k := range keys {
			if _, ok := o[k]; !ok {
				return nil, bad
			}
		}
		if o["schema"] != float64(1) || o["kind"] != "heap-mount-observation" {
			return nil, bad
		}
		for _, k := range []string{"id", "name", "image", "user", "project", "service", "oneoff", "owner", "initializer", "working_dir"} {
			if _, ok := o[k].(string); !ok {
				return nil, bad
			}
		}
		number := func(v any) bool { n, ok := v.(float64); return ok && n >= 0 && n == math.Trunc(n) }
		if !number(o["restart_count"]) {
			return nil, bad
		}
		hc, st := m(o["host_config"]), m(o["state"])
		hcNames := map[string]string{"network_mode": "NetworkMode", "binds": "Binds", "mounts": "Mounts", "readonly_rootfs": "ReadonlyRootfs", "privileged": "Privileged", "auto_remove": "AutoRemove", "cap_add": "CapAdd", "cap_drop": "CapDrop", "security_opt": "SecurityOpt", "pid_mode": "PidMode", "ipc_mode": "IpcMode", "uts_mode": "UTSMode", "userns_mode": "UsernsMode", "restart_policy": "RestartPolicy", "tmpfs": "Tmpfs"}
		stateNames := map[string]string{"status": "Status", "running": "Running", "pid": "Pid", "started_at": "StartedAt", "exit_code": "ExitCode", "oom_killed": "OOMKilled", "restarting": "Restarting", "paused": "Paused"}
		if len(hc) != len(hcNames) || len(st) != len(stateNames) {
			return nil, bad
		}
		hostOut, stateOut := map[string]any{}, map[string]any{}
		for k, name := range hcNames {
			v, ok := hc[k]
			if !ok {
				return nil, bad
			}
			hostOut[name] = v
		}
		for k, name := range stateNames {
			v, ok := st[k]
			if !ok {
				return nil, bad
			}
			stateOut[name] = v
		}
		for _, k := range []string{"readonly_rootfs", "privileged", "auto_remove"} {
			if _, ok := hc[k].(bool); !ok {
				return nil, bad
			}
		}
		for _, k := range []string{"network_mode", "pid_mode", "ipc_mode", "uts_mode", "userns_mode"} {
			if _, ok := hc[k].(string); !ok {
				return nil, bad
			}
		}
		for _, k := range []string{"running", "oom_killed", "restarting", "paused"} {
			if _, ok := st[k].(bool); !ok {
				return nil, bad
			}
		}
		for _, k := range []string{"status", "started_at"} {
			if _, ok := st[k].(string); !ok {
				return nil, bad
			}
		}
		if !number(st["pid"]) || st["pid"].(float64) > math.MaxInt32 || !number(st["exit_code"]) || st["exit_code"].(float64) > 255 || st["restarting"] != false || st["paused"] != false {
			return nil, bad
		}
		for _, k := range []string{"entrypoint", "cmd"} {
			if o[k] != nil {
				vs, ok := o[k].([]any)
				if !ok {
					return nil, bad
				}
				for _, v := range vs {
					if _, ok := v.(string); !ok {
						return nil, bad
					}
				}
			}
		}
		return map[string]any{"Id": o["id"], "Name": o["name"], "Image": o["image"], "Config": map[string]any{"User": o["user"], "Labels": map[string]any{"com.docker.compose.project": o["project"], "com.docker.compose.service": o["service"], "com.docker.compose.oneoff": o["oneoff"], "lantern.owner": o["owner"], "lantern.initializer": o["initializer"]}, "Entrypoint": o["entrypoint"], "Cmd": o["cmd"], "WorkingDir": o["working_dir"]}, "HostConfig": hostOut, "Mounts": o["mounts"], "RestartCount": o["restart_count"], "State": stateOut}, nil
	}
	profile := func(o map[string]any, cid, state string) bool {
		conf, st := m(o["Config"]), m(o["State"])
		labels := m(conf["Labels"])
		ok := o["Id"] == cid && o["Image"] == ctx.ImageID && conf["User"] == ctx.RuntimeUser && labels["com.docker.compose.service"] == bind.Service && labels["com.docker.compose.project"] == ctx.RunID && o["RestartCount"] == float64(0) && st["Status"] == state && st["Running"] == (state == "running")
		if state == "created" {
			ok = ok && (st["StartedAt"] == "" || st["StartedAt"] == "0001-01-01T00:00:00Z")
		}
		if state == "exited" {
			ok = ok && st["ExitCode"] == float64(0) && st["OOMKilled"] == false
		}
		return ok
	}
	fingerprint := func(o map[string]any) map[string]any {
		out := map[string]any{}
		for _, k := range []string{"Id", "Name", "Image", "Config", "HostConfig", "Mounts"} {
			v, ok := o[k]
			if !ok {
				return nil
			}
			if k == "Mounts" {
				rows := append([]any{}, arr(v)...)
				sort.Slice(rows, func(i, j int) bool {
					return fmt.Sprint(m(rows[i])["Destination"]) < fmt.Sprint(m(rows[j])["Destination"])
				})
				v = rows
			}
			out[k] = v
		}
		return out
	}
	probeFingerprint := func(o map[string]any) (map[string]any, error) {
		// The existing initializer permits only this one default equivalence.
		// Do not rewrite raw evidence or conflate numeric zero with false.
		value := m(o["HostConfig"])["OomKillDisable"]
		if value == nil {
			value = false
		} else if _, ok := value.(bool); !ok {
			return nil, bad
		}
		result := fingerprint(o)
		if result == nil {
			return nil, bad
		}
		host := map[string]any{}
		for k, v := range m(result["HostConfig"]) {
			host[k] = v
		}
		host["OomKillDisable"] = value
		result["HostConfig"] = host
		return result, nil
	}
	target := "/run/lantern-bench-heap"
	validSource := func(v any) bool {
		s, ok := v.(string)
		if !ok || !filepath.IsAbs(s) || filepath.Clean(s) != s {
			return false
		}
		for _, c := range s {
			if c < 32 || c == 127 || c == ',' || c == '"' {
				return false
			}
		}
		return true
	}
	bindOpts := func(v any) bool {
		return eq(v, map[string]any{"Propagation": "rprivate"}) || eq(v, map[string]any{"Propagation": "rprivate", "CreateMountpoint": false})
	}
	heapRows := func(o map[string]any, allowed map[string]bool) (map[string]any, map[string]any, error) {
		reqs, effs := arr(m(o["HostConfig"])["Mounts"]), arr(o["Mounts"])
		if len(reqs) != 3 || len(effs) != 5 {
			return nil, nil, bad
		}
		volumes := map[string]bool{}
		for _, v := range reqs {
			r := m(v)
			if r["Type"] == "volume" {
				target, ok := r["Target"].(string)
				source, sok := r["Source"].(string)
				if !ok || !sok || volumes[target] || (target != "/data" && target != "/state") || len(r) != 4 || !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]+$`).MatchString(source) || !eq(r["VolumeOptions"], map[string]any{"NoCopy": true}) {
					return nil, nil, bad
				}
				volumes[target] = true
			}
		}
		if len(volumes) != 2 {
			return nil, nil, bad
		}
		binds, ok := m(o["HostConfig"])["Binds"].([]any)
		if !ok || len(binds) != 2 {
			return nil, nil, bad
		}
		bindTargets := map[string]bool{}
		for _, v := range binds {
			b, ok := v.(string)
			if !ok {
				return nil, nil, bad
			}
			parts := strings.Split(b, ":")
			if len(parts) != 3 || !strings.HasPrefix(parts[0], "/") || parts[2] != "ro" || bindTargets[parts[1]] || (parts[1] != "/etc/ntp.conf" && parts[1] != "/run/lantern-config") {
				return nil, nil, bad
			}
			bindTargets[parts[1]] = true
		}
		var a, b map[string]any
		for _, v := range reqs {
			r := m(v)
			if r["Target"] == target {
				if a != nil {
					return nil, nil, bad
				}
				a = r
			}
		}
		for _, v := range effs {
			r := m(v)
			if r["Destination"] == target {
				if b != nil {
					return nil, nil, bad
				}
				b = r
			}
		}
		if a == nil || b == nil || (len(a) != 4 && (len(a) != 5 || a["ReadOnly"] != false)) || a["Type"] != "bind" || !bindOpts(a["BindOptions"]) || len(b) != 6 || b["Type"] != "bind" || b["RW"] != true || (b["Mode"] != "" && b["Mode"] != "rw") || b["Propagation"] != "rprivate" || !validSource(a["Source"]) || !validSource(b["Source"]) {
			return nil, nil, bad
		}
		for _, r := range []map[string]any{a, b} {
			if allowed != nil && !allowed[r["Source"].(string)] {
				return nil, nil, bad
			}
		}
		return a, b, nil
	}
	sameServing := func(left, right map[string]any) bool {
		for _, k := range []string{"Id", "Name", "Image", "Config"} {
			if !eq(left[k], right[k]) {
				return false
			}
		}
		lh, rh := m(left["HostConfig"]), m(right["HostConfig"])
		if len(lh) != len(rh) {
			return false
		}
		for k, v := range lh {
			if k != "Mounts" && !eq(v, rh[k]) {
				return false
			}
		}
		for _, group := range []struct {
			left, right []any
			key         string
		}{{arr(lh["Mounts"]), arr(rh["Mounts"]), "Target"}, {arr(left["Mounts"]), arr(right["Mounts"]), "Destination"}} {
			if len(group.left) != len(group.right) {
				return false
			}
			for _, v := range group.left {
				a := m(v)
				matches := 0
				for _, w := range group.right {
					b := m(w)
					if a[group.key] != b[group.key] {
						continue
					}
					matches++
					if a[group.key] == target {
						if len(a) != len(b) {
							return false
						}
						for k, x := range a {
							if k != "Source" && !eq(x, b[k]) {
								return false
							}
						}
					} else if !eq(a, b) {
						return false
					}
				}
				if matches != 1 {
					return false
				}
			}
		}
		return true
	}
	original, e := serving(bind.OriginalBeforeFile)
	if e != nil {
		return bad
	}
	after, e := serving(bind.OriginalAfterFile)
	if e != nil {
		return bad
	}
	cid := bind.Session.ContainerID
	if !profile(original, cid, "created") || !profile(after, cid, "created") || fingerprint(original) == nil || !eq(fingerprint(original), fingerprint(after)) {
		return bad
	}
	labels := m(m(original["Config"])["Labels"])
	if original["Name"] != "/"+ctx.RunID+"-"+bind.Service+"-1" || labels["com.docker.compose.oneoff"] != "False" || labels["lantern.owner"] != "" || labels["lantern.initializer"] != "" {
		return bad
	}
	a, b, e := heapRows(original, nil)
	if e != nil {
		return bad
	}
	allowed := map[string]bool{bind.CanonicalDirectory: true, a["Source"].(string): true, b["Source"].(string): true}
	sources := []string{}
	for s := range allowed {
		if s != bind.CanonicalDirectory {
			sources = append(sources, s)
		}
	}
	sort.Strings(sources)
	var pairs []retainedMountPair
	pairRaw, pairErr := raw(bind.PairsFile)
	if pairErr != nil || !strings.HasPrefix(strings.TrimSpace(string(pairRaw)), "[") {
		return bad
	}
	if decode(bind.PairsFile, &pairs) != nil || len(pairs) != len(sources) || len(pairs) > 2 {
		return bad
	}
	for i, s := range sources {
		if pairs[i] != (retainedMountPair{Destination: target, DeclaredSource: bind.CanonicalDirectory, OriginalSource: bind.CanonicalDirectory, CloneSource: s, ExpectedType: "directory"}) {
			return bad
		}
	}
	if len(pairs) == 0 {
		if string(bind.Probe) != "null" {
			return bad
		}
	} else {
		var probe retainedMountProbe
		if benchheap.Decode(bind.Probe, &probe, 128<<10) != nil {
			return bad
		}
		for ref, name := range map[*retainedHeapReference]string{&probe.CreatedFile: "probe-created.json", &probe.CompletedFile: "probe-completed.json", &probe.OutputFile: "probe-output.log", &probe.WaitFile: "probe-wait.log", &probe.RemoveFile: "probe-remove.log", &probe.EnvironmentFile: "image-environment.json"} {
			if ref.Path != filepath.Join(base, name) {
				return bad
			}
		}
		created, e := object(probe.CreatedFile)
		if e != nil {
			return bad
		}
		completed, e := object(probe.CompletedFile)
		if e != nil {
			return bad
		}
		pcid, ok := created["Id"].(string)
		if !ok || !retainedHex(pcid, 64) || pcid == cid {
			return bad
		}
		var environment []string
		envRaw, envErr := raw(probe.EnvironmentFile)
		if envErr != nil || !strings.HasPrefix(strings.TrimSpace(string(envRaw)), "[") {
			return bad
		}
		if decode(probe.EnvironmentFile, &environment) != nil {
			return bad
		}
		envSeen := map[string]bool{}
		for _, v := range environment {
			key, _, found := strings.Cut(v, "=")
			if !found || envSeen[key] || !map[string]bool{"PATH": true, "LANTERN_DEFAULT_TTL_SECONDS": true, "LANTERN_PORT": true, "LANTERN_METRICS_ADDR": true, "LANTERN_LOG_FORMAT": true, "LANTERN_LOG_LEVEL": true, "LANTERN_VERSION": true, "LANTERN_COMMIT": true}[key] {
				return bad
			}
			for _, c := range v {
				if c < 32 || c == 127 {
					return bad
				}
			}
			envSeen[key] = true
		}
		targets := []string{}
		for i := range pairs {
			targets = append(targets, fmt.Sprintf("/proof/original-%d", i), fmt.Sprintf("/proof/clone-%d", i))
		}
		stringList := func(v any, expected []string) bool {
			vs := arr(v)
			if len(vs) != len(expected) {
				return false
			}
			for i, s := range expected {
				if vs[i] != s {
					return false
				}
			}
			return true
		}
		for i, o := range []map[string]any{created, completed} {
			conf, st, hc := m(o["Config"]), m(o["State"]), m(o["HostConfig"])
			state := "created"
			if i == 1 {
				state = "exited"
			}
			env := []string{}
			for _, v := range arr(conf["Env"]) {
				s, ok := v.(string)
				if !ok {
					return bad
				}
				env = append(env, s)
			}
			sort.Strings(env)
			expectedEnv := append([]string{}, environment...)
			sort.Strings(expectedEnv)
			if o["Id"] != pcid || o["Image"] != ctx.ImageID || conf["Image"] != ctx.ImageID || conf["User"] != ctx.RuntimeUser || !stringList(conf["Entrypoint"], []string{"/bin/busybox"}) || !stringList(conf["Cmd"], append([]string{"stat", "-c", "%d:%i:%F"}, targets...)) || !eq(env, expectedEnv) || st["Status"] != state || st["Running"] != false || hc["NetworkMode"] != "none" || hc["ReadonlyRootfs"] != true || hc["Privileged"] != false || hc["AutoRemove"] != false || !stringList(hc["CapDrop"], []string{"ALL"}) || !stringList(hc["SecurityOpt"], []string{"no-new-privileges:true"}) || hc["Memory"] != float64(33554432) || hc["NanoCpus"] != float64(250000000) || hc["PidsLimit"] != float64(16) || !eq(hc["RestartPolicy"], map[string]any{"Name": "no", "MaximumRetryCount": float64(0)}) {
				return bad
			}
			for _, k := range []string{"CapAdd", "Binds", "VolumesFrom", "Devices", "DeviceRequests", "DeviceCgroupRules"} {
				if hc[k] != nil {
					vs, ok := hc[k].([]any)
					if !ok || len(vs) != 0 {
						return bad
					}
				}
			}
			for _, v := range []any{hc["Tmpfs"], hc["PortBindings"], conf["Volumes"]} {
				if v != nil && (m(v) == nil || len(m(v)) != 0) {
					return bad
				}
			}
			for _, k := range []string{"PidMode", "UTSMode", "UsernsMode"} {
				if hc[k] != "" {
					return bad
				}
			}
			if hc["PublishAllPorts"] != false {
				return bad
			}
			reqs, effs := arr(hc["Mounts"]), arr(o["Mounts"])
			if len(reqs) != len(targets) || len(effs) != len(targets) {
				return bad
			}
			for j, t := range targets {
				var req, eff map[string]any
				for _, v := range reqs {
					r := m(v)
					if r["Target"] == t {
						if req != nil {
							return bad
						}
						req = r
					}
				}
				for _, v := range effs {
					r := m(v)
					if r["Destination"] == t {
						if eff != nil {
							return bad
						}
						eff = r
					}
				}
				pair := pairs[j/2]
				inPair := func(v any) bool { return v == pair.OriginalSource || v == pair.CloneSource }
				if req == nil || eff == nil || len(req) != 5 || req["Type"] != "bind" || req["ReadOnly"] != true || !inPair(req["Source"]) || !bindOpts(req["BindOptions"]) || len(eff) != 6 || eff["Type"] != "bind" || eff["RW"] != false || !inPair(eff["Source"]) || (eff["Mode"] != "" && eff["Mode"] != "ro") || eff["Propagation"] != "rprivate" {
					return bad
				}
			}
		}
		labels := m(m(created["Config"])["Labels"])
		nonce, _ := labels["lantern.initializer"].(string)
		if created["Name"] != "/"+ctx.RunID+"-"+bind.Service+"-bind-proof" || labels["com.docker.compose.project"] != ctx.RunID || labels["com.docker.compose.service"] != bind.Service+"-bind-proof" || labels["com.docker.compose.oneoff"] != "True" || labels["lantern.owner"] != "compose-bind-identity-probe" || !regexp.MustCompile(`^lantern-owned-init\.[A-Za-z0-9]+$`).MatchString(nonce) || created["RestartCount"] != float64(0) || completed["RestartCount"] != float64(0) || (m(created["State"])["StartedAt"] != "" && m(created["State"])["StartedAt"] != "0001-01-01T00:00:00Z") {
			return bad
		}
		createdFingerprint, createdErr := probeFingerprint(created)
		completedFingerprint, completedErr := probeFingerprint(completed)
		if createdErr != nil || completedErr != nil || !eq(createdFingerprint, completedFingerprint) || m(completed["State"])["ExitCode"] != float64(0) || m(completed["State"])["OOMKilled"] != false {
			return bad
		}
		wait, e := raw(probe.WaitFile)
		if e != nil || string(wait) != "0\n" {
			return bad
		}
		removed, e := raw(probe.RemoveFile)
		if e != nil || string(removed) != pcid+"\n" {
			return bad
		}
		output, e := raw(probe.OutputFile)
		if e != nil || len(output) > 4096 {
			return bad
		}
		rows := strings.Split(string(output), "\n")
		if len(rows) != len(targets)+1 || rows[len(rows)-1] != "" {
			return bad
		}
		for i := 0; i < len(targets); i += 2 {
			parts := strings.Split(rows[i], ":")
			if len(parts) != 3 || parts[2] != "directory" || rows[i] != rows[i+1] {
				return bad
			}
			for _, s := range parts[:2] {
				if s == "" || s[0] == '0' {
					return bad
				}
				for _, c := range s {
					if c < '0' || c > '9' {
						return bad
					}
				}
			}
		}
	}
	clone, e := serving(bind.CloneBeforeFile)
	if e != nil {
		return bad
	}
	cloneAfter, e := serving(bind.CloneAfterFile)
	if e != nil {
		return bad
	}
	cloneID, ok := clone["Id"].(string)
	if !ok || !retainedHex(cloneID, 64) || cloneID == cid || !profile(clone, cloneID, "created") || !profile(cloneAfter, cloneID, "exited") || fingerprint(clone) == nil || !eq(fingerprint(clone), fingerprint(cloneAfter)) {
		return bad
	}
	labels = m(m(clone["Config"])["Labels"])
	nonce, _ := labels["lantern.initializer"].(string)
	if clone["Name"] != "/"+ctx.RunID+"-"+bind.Service+"-security-init" || labels["com.docker.compose.oneoff"] != "True" || labels["lantern.owner"] != "compose-security-initializer" || !regexp.MustCompile(`^lantern-owned-init\.[A-Za-z0-9]+$`).MatchString(nonce) {
		return bad
	}
	for _, key := range []string{"User", "WorkingDir"} {
		if !eq(m(clone["Config"])[key], m(original["Config"])[key]) {
			return bad
		}
	}
	if !eq(m(clone["Config"])["Entrypoint"], []any{"/app/lantern"}) || !eq(m(clone["Config"])["Cmd"], []any{"init-security"}) || m(clone["HostConfig"])["NetworkMode"] != m(original["HostConfig"])["NetworkMode"] {
		return bad
	}
	oh, ch := m(original["HostConfig"]), m(clone["HostConfig"])
	if len(oh) != len(ch) {
		return bad
	}
	for k, v := range oh {
		if k != "Binds" && k != "Mounts" && !eq(v, ch[k]) {
			return bad
		}
	}
	for _, group := range []struct {
		left, right []any
		key         string
	}{{arr(oh["Mounts"]), arr(ch["Mounts"]), "Target"}, {arr(original["Mounts"]), arr(clone["Mounts"]), "Destination"}} {
		if len(group.left) != len(group.right) {
			return bad
		}
		for _, v := range group.left {
			a := m(v)
			matches := 0
			for _, w := range group.right {
				b := m(w)
				if a[group.key] != b[group.key] {
					continue
				}
				matches++
				if a[group.key] == target || a[group.key] == "/run/lantern-config" || a[group.key] == "/etc/ntp.conf" {
					if len(a) != len(b) {
						return bad
					}
					for k, x := range a {
						if k != "Source" && !eq(x, b[k]) {
							return bad
						}
					}
				} else if !eq(a, b) {
					return bad
				}
			}
			if matches != 1 {
				return bad
			}
		}
	}
	ca, cb, e := heapRows(clone, allowed)
	if e != nil {
		return bad
	}
	withoutSource := func(o map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range o {
			if k != "Source" {
				out[k] = v
			}
		}
		return out
	}
	if !eq(withoutSource(a), withoutSource(ca)) || !eq(withoutSource(b), withoutSource(cb)) {
		return bad
	}
	var cutPID any
	var cutStart any
	for i, ref := range []retainedHeapReference{node.MountInspectBeforeFile, node.MountInspectAfterFile} {
		var snap retainedMountSnapshot
		if ref.Path != filepath.Join(base, phase+"."+[]string{"before", "after"}[i]+".json") {
			return bad
		}
		if decode(ref, &snap) != nil || snap.Schema != 1 || snap.Protocol != benchheap.Protocol || snap.Phase != phase || snap.Service != bind.Service || snap.HostIdentity != identity || snap.Position != []string{"before", "after"}[i] {
			return bad
		}
		if snap.InspectFile.Path != filepath.Join(base, phase+"."+snap.Position+".inspect.json") || snap.DaemonFile.Path != filepath.Join(base, phase+"."+snap.Position+".daemon") {
			return bad
		}
		daemonRaw, e := raw(snap.DaemonFile)
		if e != nil || string(daemonRaw) != daemon {
			return bad
		}
		observed, e := serving(snap.InspectFile)
		if e != nil || !profile(observed, cid, "running") || !sameServing(original, observed) {
			return bad
		}
		if _, _, e = heapRows(observed, allowed); e != nil {
			return bad
		}
		st := m(observed["State"])
		pid, ok := st["Pid"].(float64)
		if !ok || pid <= 0 || st["StartedAt"] == "" || st["StartedAt"] == "0001-01-01T00:00:00Z" || (i > 0 && (cutPID != st["Pid"] || cutStart != st["StartedAt"])) {
			return bad
		}
		cutPID, cutStart = st["Pid"], st["StartedAt"]
	}
	entries, e := os.ReadDir(base)
	if e != nil || len(entries) > 32 {
		return bad
	}
	dirInfo, e := os.Lstat(base)
	if e != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm() != 0o700 {
		return bad
	}
	total := int64(0)
	for _, entry := range entries {
		info, e := os.Lstat(filepath.Join(base, entry.Name()))
		if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || !retainedEvidenceMetadataOK(info) || info.Size() > 128<<10 {
			return bad
		}
		total += info.Size()
	}
	if total > 2<<20 {
		return bad
	}
	return nil
}

func sameRetainedMountProcess(pre, post retainedHeapNode) error {
	var processPID any
	var processStart any
	for _, mountRef := range []retainedHeapReference{pre.MountInspectBeforeFile, pre.MountInspectAfterFile, post.MountInspectBeforeFile, post.MountInspectAfterFile} {
		var snap retainedMountSnapshot
		if err := readRetainedReference(mountRef, mountRef.Path, 128<<10, &snap); err != nil {
			return err
		}
		data, err := readRetainedFile(snap.InspectFile.Path, 128<<10)
		if err != nil || retainedReference(snap.InspectFile.Path, data) != snap.InspectFile {
			return errors.New("mount process raw changed")
		}
		var observed map[string]any
		if json.Unmarshal(data, &observed) != nil {
			return errors.New("mount process raw invalid")
		}
		st, _ := observed["state"].(map[string]any)
		if processPID != nil && (processPID != st["pid"] || processStart != st["started_at"]) {
			return errors.New("mount process changed between cuts")
		}
		processPID, processStart = st["pid"], st["started_at"]
	}
	return nil
}

// These compact facts have already been semantically checked above; reopen
// their hashes to keep cohort checks bound to the same immutable raw bytes.
func retainedMountFacts(node retainedHeapNode) (string, string, error) {
	data, err := readRetainedFile(node.MountBindingFile.Path, 128<<10)
	if err != nil || retainedReference(node.MountBindingFile.Path, data) != node.MountBindingFile {
		return "", "", errors.New("mount cohort binder changed")
	}
	var binding retainedMountBinding
	if json.Unmarshal(data, &binding) != nil {
		return "", "", errors.New("mount cohort binder invalid")
	}
	var host retainedMountHost
	if err := readRetainedReference(binding.HostPrecreateFile, binding.HostPrecreateFile.Path, 128<<10, &host); err != nil {
		return "", "", err
	}
	return binding.DaemonID, host.Device + ":" + host.Inode, nil
}
