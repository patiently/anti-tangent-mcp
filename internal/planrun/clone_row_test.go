package planrun

import "testing"

func TestCloneRowSharesNoMutableState(t *testing.T) {
	added := 3
	row := TaskRow{
		Categories: map[string]int{"correctness": 1},
		Criteria:   map[string]int{"test_value": 1},
		TestsAdded: &added,
	}
	cp := CloneRow(row)
	cp.Categories["correctness"] = 9
	cp.Criteria["test_value"] = 9
	*cp.TestsAdded = 9
	if row.Categories["correctness"] != 1 {
		t.Errorf("CloneRow aliased Categories: original now %v", row.Categories)
	}
	if row.Criteria["test_value"] != 1 {
		t.Errorf("CloneRow aliased Criteria: original now %v", row.Criteria)
	}
	if *row.TestsAdded != 3 {
		t.Errorf("CloneRow aliased TestsAdded: original now %d", *row.TestsAdded)
	}
}
