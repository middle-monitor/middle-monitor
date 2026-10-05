package profileflame

import (
	"bytes"
	"testing"

	"github.com/google/pprof/profile"
)

func makeProfile(t *testing.T, samples []*profile.Sample, functions []*profile.Function, locations []*profile.Location) []byte {
	t.Helper()
	p := &profile.Profile{
		SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}},
		Function:   functions,
		Location:   locations,
		Sample:     samples,
	}
	var buf bytes.Buffer
	if err := p.Write(&buf); err != nil {
		t.Fatalf("failed to write profile: %v", err)
	}
	return buf.Bytes()
}

func TestToFlameTree_InvalidData(t *testing.T) {
	_, err := ToFlameTree([]byte("not valid pprof data"))
	if err == nil {
		t.Fatal("expected error for invalid pprof data")
	}
}

func TestToFlameTree_EmptySamples(t *testing.T) {
	data := makeProfile(t, nil, nil, nil)
	tree, err := ToFlameTree(data)
	if err != nil {
		t.Fatal(err)
	}
	if tree != nil {
		t.Fatal("expected nil tree for empty samples")
	}
}

func TestToFlameTree_ValidSinglePath(t *testing.T) {
	fn := &profile.Function{ID: 1, Name: "main.handler"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn, Line: 10}}}
	sample := &profile.Sample{Location: []*profile.Location{loc}, Value: []int64{100}}

	data := makeProfile(t, []*profile.Sample{sample}, []*profile.Function{fn}, []*profile.Location{loc})
	tree, err := ToFlameTree(data)
	if err != nil {
		t.Fatal(err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
	if tree.Name != "root" {
		t.Fatalf("expected root, got %q", tree.Name)
	}
	if len(tree.Children) == 0 {
		t.Fatal("expected children")
	}
}

func TestToFlameTree_SampleWithNoLocations(t *testing.T) {
	// Sample with empty Location slice is skipped
	fn := &profile.Function{ID: 1, Name: "main.run"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn}}}
	sampleGood := &profile.Sample{Location: []*profile.Location{loc}, Value: []int64{50}}
	sampleBad := &profile.Sample{Location: []*profile.Location{}, Value: []int64{10}}

	data := makeProfile(t,
		[]*profile.Sample{sampleGood, sampleBad},
		[]*profile.Function{fn},
		[]*profile.Location{loc},
	)
	tree, err := ToFlameTree(data)
	if err != nil {
		t.Fatal(err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
}

func TestToFlameTree_LocationWithNoLine(t *testing.T) {
	// Location with no Line → function name falls back to "?"
	loc := &profile.Location{ID: 1, Line: []profile.Line{}}
	sample := &profile.Sample{Location: []*profile.Location{loc}, Value: []int64{30}}

	data := makeProfile(t, []*profile.Sample{sample}, nil, []*profile.Location{loc})
	tree, err := ToFlameTree(data)
	if err != nil {
		t.Fatal(err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
}

func TestToFlameTree_SampleValueZero(t *testing.T) {
	fn := &profile.Function{ID: 1, Name: "main.run"}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn}}}
	// Sample with Value[0] == 0 is skipped; need a second non-zero sample to get a tree
	sampleZero := &profile.Sample{Location: []*profile.Location{loc}, Value: []int64{0}}
	sampleNonZero := &profile.Sample{Location: []*profile.Location{loc}, Value: []int64{10}}

	data := makeProfile(t,
		[]*profile.Sample{sampleZero, sampleNonZero},
		[]*profile.Function{fn},
		[]*profile.Location{loc},
	)
	tree, err := ToFlameTree(data)
	if err != nil {
		t.Fatal(err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
}

func TestToFlameTree_MultiplePaths_Merging(t *testing.T) {
	fn1 := &profile.Function{ID: 1, Name: "main.handler"}
	fn2 := &profile.Function{ID: 2, Name: "db.query"}
	fn3 := &profile.Function{ID: 3, Name: "http.serve"}

	loc1 := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn1}}}
	loc2 := &profile.Location{ID: 2, Line: []profile.Line{{Function: fn2}}}
	loc3 := &profile.Location{ID: 3, Line: []profile.Line{{Function: fn3}}}

	// Two samples sharing a path prefix
	s1 := &profile.Sample{Location: []*profile.Location{loc2, loc1}, Value: []int64{80}}
	s2 := &profile.Sample{Location: []*profile.Location{loc3, loc1}, Value: []int64{20}}

	data := makeProfile(t,
		[]*profile.Sample{s1, s2},
		[]*profile.Function{fn1, fn2, fn3},
		[]*profile.Location{loc1, loc2, loc3},
	)
	tree, err := ToFlameTree(data)
	if err != nil {
		t.Fatal(err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
	// Root should have children
	if len(tree.Children) == 0 {
		t.Fatal("expected root to have children")
	}
	// Children sorted by value desc
	for i := 1; i < len(tree.Children); i++ {
		if tree.Children[i].Value > tree.Children[i-1].Value {
			t.Fatal("children not sorted by value desc")
		}
	}
}

func TestToFlameTree_LocationWithEmptyFunctionName(t *testing.T) {
	// Function exists but name is empty → falls back to "?"
	fn := &profile.Function{ID: 1, Name: ""}
	loc := &profile.Location{ID: 1, Line: []profile.Line{{Function: fn}}}
	sample := &profile.Sample{Location: []*profile.Location{loc}, Value: []int64{15}}

	data := makeProfile(t, []*profile.Sample{sample}, []*profile.Function{fn}, []*profile.Location{loc})
	tree, err := ToFlameTree(data)
	if err != nil {
		t.Fatal(err)
	}
	if tree == nil {
		t.Fatal("expected non-nil tree")
	}
}
