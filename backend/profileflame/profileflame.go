package profileflame

import (
	"bytes"
	"sort"

	"github.com/google/pprof/profile"
)

// FlameNode is the JSON shape expected by d3-flame-graph and similar viewers.
// Unit and ValueType describe what Value measures (e.g. "bytes"/"alloc_space",
// "count"/"alloc_objects", "nanoseconds"/"cpu") and are only set on the root node.
type FlameNode struct {
	Name      string      `json:"name"`
	Value     int64       `json:"value"`
	Unit      string      `json:"unit,omitempty"`
	ValueType string      `json:"valueType,omitempty"`
	Children  []FlameNode `json:"children,omitempty"`
}

// ToFlameTree parses pprof data and returns a flame graph tree (root with children).
// Returns nil, nil if the profile has no samples or cannot be parsed.
func ToFlameTree(data []byte) (*FlameNode, error) {
	p, err := profile.Parse(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if len(p.Sample) == 0 {
		return nil, nil
	}

	// Tree keyed by path (for merging). We'll use a mutable tree then convert to FlameNode.
	type node struct {
		name     string
		value    int64
		children map[string]*node
	}
	root := &node{name: "root", children: make(map[string]*node)}

	for _, s := range p.Sample {
		if len(s.Location) == 0 {
			continue
		}
		// Build stack of function names (leaf first in pprof, we want root first for tree)
		names := make([]string, 0, len(s.Location))
		for _, loc := range s.Location {
			name := "?"
			if len(loc.Line) > 0 && loc.Line[0].Function != nil && loc.Line[0].Function.Name != "" {
				name = loc.Line[0].Function.Name
			}
			names = append(names, name)
		}
		// Reverse so root (top of stack) is first
		for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
			names[i], names[j] = names[j], names[i]
		}
		// Sample value: use first value type (e.g. cpu nanoseconds, alloc bytes)
		var v int64
		if len(s.Value) > 0 {
			v = s.Value[0]
		}
		if v == 0 {
			continue
		}
		// Add v to every node along the path (inclusive)
		cur := root
		for _, n := range names {
			cur.value += v
			if cur.children[n] == nil {
				cur.children[n] = &node{name: n, children: make(map[string]*node)}
			}
			cur = cur.children[n]
		}
		cur.value += v // leaf
	}

	// Convert to FlameNode (recursive), sort children by value desc
	var toFlame func(n *node) FlameNode
	toFlame = func(n *node) FlameNode {
		out := FlameNode{Name: n.name, Value: n.value}
		if len(n.children) == 0 {
			return out
		}
		children := make([]FlameNode, 0, len(n.children))
		for _, c := range n.children {
			children = append(children, toFlame(c))
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Value > children[j].Value })
		out.Children = children
		return out
	}

	tree := toFlame(root)
	// Describe what Value[0] measures so the UI can label/format it correctly.
	if len(p.SampleType) > 0 && p.SampleType[0] != nil {
		tree.ValueType = p.SampleType[0].Type
		tree.Unit = p.SampleType[0].Unit
	}
	return &tree, nil
}
