package registry

import (
	"strings"

	"github.com/shikejing/schema-registry-go/schema"
)

const timeFormat = "2006-01-02T15:04:05.999999999Z07:00"

// refKey identifies a concrete referenced version.
type refKey struct {
	subject string
	version int
}

// outgoingRefsLocked returns the pinned outgoing references of a stored
// version. Callers hold mu.
func (r *Registry) outgoingRefsLocked(v *schema.Version) []refKey {
	out := make([]refKey, 0, len(v.Refs))
	for _, ref := range v.Refs {
		out = append(out, refKey{ref.Subject, ref.Version})
	}
	return out
}

// candidateRefs extracts pinned refs from an in-flight candidate schema
// (cloneAndPin has already resolved them).
func candidateRefs(s *schema.Schema) []refKey {
	var out []refKey
	seen := map[refKey]bool{}
	var walk func(f *schema.Field)
	walk = func(f *schema.Field) {
		if f == nil {
			return
		}
		if f.Ref != nil {
			k := refKey{f.Ref.Subject, f.Ref.Version}
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
		for _, p := range f.Properties {
			walk(p)
		}
		if f.Items != nil {
			walk(f.Items)
		}
	}
	for _, f := range s.Fields {
		walk(f)
	}
	return out
}

// detectCycle performs an iterative depth-first search over the reference
// graph that would exist once candidate is committed as newSubject. It
// returns the subject path of the first cycle found, or nil if the graph
// remains acyclic. A self-reference also counts as a cycle.
//
// Only the latest version of each existing subject participates in the
// graph: new registrations pin references to tips that are current at
// registration time, so a cycle can only run through current tips. Older
// versions may be referenced transitively but cannot lead to the new
// subject.
func (r *Registry) detectCycle(newSubject string, candidate *schema.Schema) []string {
	// Build subject-level adjacency from current tips.
	adj := map[string][]string{}
	for subject, st := range r.snap.Subjects {
		if len(st.Versions) == 0 {
			continue
		}
		tip := st.Versions[len(st.Versions)-1]
		neighbors := adj[subject]
		for _, ref := range tip.Refs {
			neighbors = append(neighbors, ref.Subject)
		}
		adj[subject] = neighbors
	}
	// Insert the in-flight candidate as a virtual node (not stored yet).
	start := newSubject
	var startNeighbors []string
	for _, ref := range candidateRefs(candidate) {
		startNeighbors = append(startNeighbors, ref.subject)
	}
	adj[start] = startNeighbors
	if containsString(startNeighbors, start) {
		return []string{start, start}
	}

	const white, gray, black = 0, 1, 2
	color := map[string]int{}
	parent := map[string]string{}

	var dfs func(u string) []string
	dfs = func(u string) []string {
		color[u] = gray
		for _, w := range adj[u] {
			switch color[w] {
			case white:
				parent[w] = u
				if cyc := dfs(w); cyc != nil {
					return cyc
				}
			case gray:
				return buildCyclePath(parent, u, w)
			}
		}
		color[u] = black
		return nil
	}
	return dfs(start)
}

// buildCyclePath reconstructs the cyclic subject path as w -> ... -> u -> w
// from the DFS parent links.
func buildCyclePath(parent map[string]string, u, w string) []string {
	chain := []string{u}
	for cur := u; cur != w; {
		p, ok := parent[cur]
		if !ok {
			break
		}
		chain = append([]string{p}, chain...)
		cur = p
	}
	chain = append(chain, w)
	return chain
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func formatCycle(path []string) string {
	return strings.Join(path, " -> ")
}
