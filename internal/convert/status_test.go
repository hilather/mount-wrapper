package convert_test

import (
	"testing"

	"github.com/hilather/mount-wrapper/internal/convert"
	"github.com/hilather/mount-wrapper/internal/state"
)

func TestProgressLabel(t *testing.T) {
	t.Parallel()
	if got := convert.ProgressLabel(state.StatusConverting); got != convert.ProgressLabelConverting {
		t.Fatalf("got %q", got)
	}
	if convert.ProgressLabelConverting != "converting to non-solid" {
		t.Fatalf("label=%q", convert.ProgressLabelConverting)
	}
	if convert.ProgressLabel(state.StatusIndexing) != "" {
		t.Fatal("indexing label should be empty in convert package")
	}
	if !convert.IsConvertingStatus(state.StatusConverting) {
		t.Fatal("IsConvertingStatus")
	}
}

func TestCanEnterLeaveConverting(t *testing.T) {
	t.Parallel()
	// Enter
	if !convert.CanEnterConverting(state.StatusDiscovered) {
		t.Fatal("discovered → converting")
	}
	if !convert.CanEnterConverting(state.StatusMountFailed) {
		t.Fatal("mount_failed → converting")
	}
	if !convert.CanEnterConverting(state.StatusUnmounting) {
		t.Fatal("unmounting → converting")
	}
	if convert.CanEnterConverting(state.StatusMounted) {
		t.Fatal("mounted must not enter converting")
	}
	if convert.CanEnterConverting(state.StatusIndexing) {
		t.Fatal("indexing must not enter converting")
	}
	// Leave
	for _, to := range []string{
		state.StatusDiscovered,
		state.StatusIndexing,
		state.StatusMounting,
		state.StatusMountFailed,
		state.StatusIndexFailed,
		state.StatusUnmounting,
		state.StatusAbsent,
	} {
		if !convert.CanLeaveConverting(to) {
			t.Fatalf("converting → %s should be allowed", to)
		}
	}
	if convert.CanLeaveConverting(state.StatusMounted) {
		t.Fatal("converting → mounted not allowed")
	}
}
