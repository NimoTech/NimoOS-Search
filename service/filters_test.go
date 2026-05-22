package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIntersectRoots_UserDidNotSpecify(t *testing.T) {
	got := IntersectRoots(nil, []string{"r1", "r2"})
	require.Equal(t, []string{"r1", "r2"}, got)
}

func TestIntersectRoots_UserOverlaps(t *testing.T) {
	got := IntersectRoots([]string{"r1", "r3"}, []string{"r1", "r2"})
	require.Equal(t, []string{"r1"}, got)
}

func TestIntersectRoots_NoOverlap(t *testing.T) {
	got := IntersectRoots([]string{"r9"}, []string{"r1", "r2"})
	require.Empty(t, got)
}

func TestIntersectRoots_AllowedEmpty(t *testing.T) {
	got := IntersectRoots([]string{"r1"}, nil)
	require.Empty(t, got)
}

func TestApplyScope_Empty(t *testing.T) {
	in := &Filters{RootIDs: []string{"r1"}}
	out, warn := ApplyScope(in, []string{"r2"})
	require.Empty(t, out.RootIDs)
	require.Equal(t, "no_accessible_roots", warn)
}

func TestApplyScope_PartialOverlap(t *testing.T) {
	in := &Filters{RootIDs: []string{"r1", "r3"}}
	out, warn := ApplyScope(in, []string{"r1", "r2"})
	require.Equal(t, []string{"r1"}, out.RootIDs)
	require.Equal(t, "", warn)
}
