package util

import "testing"

func TestNewReqID(t *testing.T) {
	id1 := NewReqID()
	id2 := NewReqID()
	t.Logf("id1: '%s'", id1)
	t.Logf("id2: '%s'", id2)
	if id2 == id1 {
		t.Errorf("should be different: '%v' <> '%v'", id1, id2)
	}
}
