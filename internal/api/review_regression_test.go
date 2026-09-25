package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"
)

// Uploads the service rejects (unknown note, too many photos) keep their
// bytes in the media store forever: disk is not bounded by the image limit.
func TestRegressionRejectedUploadLeavesOrphanBlob(t *testing.T) {
	handler, runtime, _ := newAITestHandler(t, "sk-or-test")
	owner := bootstrapOwner(t, handler)
	for i := 0; i < 3; i++ {
		content := append(append([]byte{}, testJPEG...), []byte(fmt.Sprintf("orphan-%d", i))...)
		response := upload(t, handler, owner.Token, "01900000-0000-7000-8000-000000000000", content, "image/jpeg", 0, "")
		if response.Code == http.StatusCreated {
			t.Fatalf("upload to a missing note accepted")
		}
		sum := sha256.Sum256(content)
		if reader, _, err := runtime.Media().Open(hex.EncodeToString(sum[:])); err == nil {
			reader.Close()
			t.Errorf("rejected upload %d (status %d) is still stored on disk", i, response.Code)
		}
	}
}
