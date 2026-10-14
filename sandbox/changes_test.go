package sandbox

import (
	"os"
	"strings"
	"testing"
)

func TestChangeReviewUnifiedDiffAndBinaryFiles(t *testing.T) {
	before := map[string]snapshotFile{"file": {Body: []byte("same\nold\nend\n"), Mode: 0600}, "deleted": {Body: []byte("gone\n"), Mode: 0600}, "mode": {Body: []byte("same"), Mode: 0600}}
	after := map[string]snapshotFile{"file": {Body: []byte("same\nnew\nend\n"), Mode: 0600}, "binary": {Body: []byte{'x', 0, 'y'}, Mode: 0600}, "large": {Body: []byte(strings.Repeat("x", 32769)), Mode: 0600}, "dir": {Mode: os.ModeDir | 0755}, "mode": {Body: []byte("same"), Mode: 0700}}
	review := changeReview(workspaceChanges(before, after), before, after)
	for _, marker := range []string{"@@", "-old\n+new", "--- original/file", "+++ sandbox/file", "+++ /dev/null", "binary content differs", "content too large", "directory: added", "file mode changed"} {
		if !strings.Contains(review, marker) {
			t.Fatalf("missing %q:\n%s", marker, review)
		}
	}
	if strings.Contains(review, "x\x00y") || strings.Contains(review, strings.Repeat("x", 100)) {
		t.Fatal("binary or oversized content was printed")
	}
}

func TestChangeReviewShowsMissingFinalNewline(t *testing.T) {
	before := map[string]snapshotFile{"file": {Body: []byte("same"), Mode: 0600}}
	after := map[string]snapshotFile{"file": {Body: []byte("same\n"), Mode: 0600}}
	review := changeReview(workspaceChanges(before, after), before, after)
	if !strings.Contains(review, "-same\n\\ No newline at end of file\n+same\n") {
		t.Fatal("newline-only change was hidden", review)
	}
}
