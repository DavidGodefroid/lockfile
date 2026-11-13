package backend

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/DavidGodefroid/lockkit/console"
	"github.com/DavidGodefroid/lockkit/sanitize"
	"github.com/DavidGodefroid/lockkit/secrets"

	"github.com/DavidGodefroid/lockfile/internal/rpc"
	"github.com/DavidGodefroid/lockfile/pluginspec"
)

// heldValue is a value the human typed for a placeholder, held in this
// console's memory only.
type heldValue struct {
	v     []byte
	typed time.Time
	// files are the files a run replayed it on (key, or handle).
	files map[string]bool
}

// vault holds the values the human typed this console session, by
// placeholder name: never written, never sent, wiped when the console
// ends (Backend.Close). A name held is reused without asking again.
type vault struct {
	mu sync.Mutex
	m  map[string]*heldValue
}

// heldInfo is what a screen says of a held value: names and counts only.
type heldInfo struct {
	typed   time.Time
	replays int
}

// info reports what the vault holds for name.
func (v *vault) info(name string) (heldInfo, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	h, ok := v.m[name]
	if !ok {
		return heldInfo{}, false
	}
	return heldInfo{typed: h.typed, replays: len(h.files)}, true
}

// names is the sorted names held.
func (v *vault) names() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]string, 0, len(v.m))
	for n := range v.m {
		out = append(out, n)
	}
	slices.Sort(out)
	return out
}

// get returns a copy of the value held for name (the caller wipes it).
func (v *vault) get(name string) []byte {
	v.mu.Lock()
	defer v.mu.Unlock()
	h, ok := v.m[name]
	if !ok {
		return nil
	}
	return slices.Clone(h.v)
}

// keep holds a copy of value for name, typed at now, unless a value is
// held for it already (the first one typed stays).
func (v *vault) keep(name string, value []byte, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.m == nil {
		v.m = map[string]*heldValue{}
	}
	if _, ok := v.m[name]; ok {
		return
	}
	v.m[name] = &heldValue{v: slices.Clone(value), typed: now, files: map[string]bool{}}
}

// replayed records that a run used name's value on file.
func (v *vault) replayed(name, file string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if h, ok := v.m[name]; ok {
		h.files[file] = true
	}
}

// wipe drops every value, zeroed first.
func (v *vault) wipe() {
	v.mu.Lock()
	defer v.mu.Unlock()
	for n, h := range v.m {
		secrets.Wipe(h.v)
		delete(v.m, n)
	}
}

// checkBindings reports whether bindings bind exactly the inputs of the
// plugins shown: every input of every plugin, and nothing else.
func checkBindings(bindings map[string]string, shown []screenPlugin) bool {
	n := 0
	for _, p := range shown {
		for _, in := range p.Manifest.Inputs {
			if _, ok := bindings[p.Name+"."+in.Name]; !ok {
				return false
			}
			n++
		}
	}
	return n == len(bindings)
}

// boundQuestion is a plugin's question with each ${input} replaced by the
// placeholder the plan binds it to.
func boundQuestion(p screenPlugin, bindings map[string]string) string {
	q := p.Manifest.Question
	for _, in := range p.Manifest.Inputs {
		if name, ok := bindings[p.Name+"."+in.Name]; ok {
			q = strings.ReplaceAll(q, "${"+in.Name+"}", "${"+name+"}")
		}
	}
	return q
}

// inputLines are the screen's lines about a plan's placeholders: the
// values the human will type on the approval, and those held already.
func inputLines(names []string, vt *vault) []string {
	var lines, ask []string
	for _, n := range names {
		h, ok := vt.info(n)
		if !ok {
			ask = append(ask, "${"+n+"}")
			continue
		}
		lines = append(lines, fmt.Sprintf("${%s}: typed at %s, reused · replayed on %d files this session", n, h.typed.Format("15:04"), h.replays))
	}
	if len(ask) > 0 {
		lines = append([]string{"values to type: " + strings.Join(ask, ", ")}, lines...)
	}
	return lines
}

// valuesFor gathers the values of a plan's placeholders: those typed for
// its ticket (typed, may be nil), else those held. missing names a
// placeholder with no value; bad one whose value fits not every input
// bound to it (with the input's kind). The caller wipes vals.
func valuesFor(names []string, typed map[string][]byte, vt *vault, bindings map[string]string, manifests map[string]pluginspec.Manifest) (vals map[string][]byte, missing, bad, kind string) {
	vals = map[string][]byte{}
	for _, n := range names {
		v, ok := typed[n]
		if ok {
			v = slices.Clone(v)
		} else if v = vt.get(n); v == nil {
			wipeValues(vals)
			return nil, n, "", ""
		}
		vals[n] = v
	}
	for k, n := range bindings {
		plugin, input, _ := rpc.SplitInputKey(k)
		in, ok := manifests[plugin].Input(input)
		if !ok || in.CheckValue(vals[n]) != nil {
			wipeValues(vals)
			return nil, "", n, in.Kind
		}
	}
	return vals, "", "", ""
}

// pluginInputs is, per plugin with inputs, the canonical JSON of its
// inputs' values ({"needle":"..."}, keys sorted), built without a Go
// string of any value so that it can be wiped.
func pluginInputs(bindings map[string]string, vals map[string][]byte) map[string][]byte {
	per := map[string]map[string][]byte{}
	for k, n := range bindings {
		plugin, input, _ := rpc.SplitInputKey(k)
		if per[plugin] == nil {
			per[plugin] = map[string][]byte{}
		}
		per[plugin][input] = vals[n]
	}
	out := make(map[string][]byte, len(per))
	for plugin, ins := range per {
		keys := make([]string, 0, len(ins))
		for k := range ins {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		b := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				b = append(b, ',')
			}
			b = appendJSONString(b, []byte(k))
			b = append(b, ':')
			b = appendJSONString(b, ins[k])
		}
		out[plugin] = append(b, '}')
	}
	return out
}

// appendJSONString appends v, valid UTF-8 without control characters
// (pluginspec.Input.CheckValue), as a JSON string.
func appendJSONString(b, v []byte) []byte {
	b = append(b, '"')
	for _, c := range v {
		if c == '"' || c == '\\' {
			b = append(b, '\\')
		}
		b = append(b, c)
	}
	return append(b, '"')
}

func wipeValues(m map[string][]byte) {
	for k, v := range m {
		secrets.Wipe(v)
		delete(m, k)
	}
}

// detailPrinter prints a plugin's human-only detail on the console
// window, escaped, when clear results are on (lockkit allows them at
// desktop level 0 only); otherwise it is dropped. Never to the agent, the
// audit or the hub.
func detailPrinter(env console.Env, planID string) func(string, []byte) {
	return func(plugin string, d []byte) {
		if !env.ShowResults() {
			return
		}
		env.Println("plan " + planID + ": " + plugin + " detail, shown here only; the agent got 1/0:")
		env.Println("  " + sanitize.Terminal(string(d), false))
	}
}
