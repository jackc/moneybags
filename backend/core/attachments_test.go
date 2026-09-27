package core_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/moneybags/backend/core"
)

func TestAttachmentMIMEHintsAndSignatures(t *testing.T) {
	for _, tc := range []struct {
		name, hint, want string
		data             []byte
	}{
		{"jpeg", "image/jpeg", "image/jpeg", []byte{0xff, 0xd8, 0xff}},
		{"normalized jpeg", "IMAGE/JPEG", "image/jpeg", []byte{0xff, 0xd8, 0xff}},
		{"inferred jpeg", "", "image/jpeg", []byte{0xff, 0xd8, 0xff}},
		{"generic hint", "application/octet-stream", "image/jpeg", []byte{0xff, 0xd8, 0xff}},
		{"png", "image/png", "image/png", []byte("\x89PNG\r\n\x1a\n")},
		{"gif", "image/gif", "image/gif", []byte("GIF89a")},
		{"webp", "image/webp", "image/webp", []byte("RIFF\x00\x00\x00\x00WEBP")},
		{"pdf", "application/pdf", "application/pdf", []byte("%PDF-1.7")},
		{"unknown binary", "", "application/octet-stream", []byte{0xad, 0xca, 0xd4, 0x52, 0, 0x41}},
		{"damaged jpeg", "image/jpeg", "", []byte{0xad, 0xca, 0xd4, 0x52, 0, 0x41}},
		{"wrong image format", "image/jpeg", "", []byte("\x89PNG\r\n\x1a\n")},
		{"html labeled png", "image/png", "", []byte("<html>not an image</html>")},
		{"text labeled gif", "image/gif", "", []byte("not a gif")},
		{"text labeled webp", "image/webp", "", []byte("not a webp")},
		{"text labeled pdf", "application/pdf", "", []byte("not a pdf")},
		{"invalid mime", "not a media type", "", []byte("receipt")},
	} {
		for _, remote := range []bool{false, true} {
			name := tc.name + "/base64"
			if remote {
				name = tc.name + "/download"
			}
			t.Run(name, func(t *testing.T) {
				_, ctx, cfg := financeFixture(t)
				blobs := &financeBlobs{data: map[string][]byte{}}
				cfg.Blobs = blobs
				cfg.Fetcher = &financeFetcher{data: tc.data}
				c := core.New(cfg)
				p := core.StageAttachmentParams{RequestID: "stage", FileName: "receipt", MIMEType: tc.hint, Data: tc.data}
				if remote {
					p.Data = nil
					p.DownloadURL = "https://files.example/receipt"
					p.FileID = "receipt"
				}
				raw, err := json.Marshal(p)
				if err != nil {
					t.Fatal(err)
				}
				out, err := c.InvokeJSON(ctx, "stage_attachment", raw)
				if tc.want == "" {
					var problem *core.Error
					if !errors.As(err, &problem) || problem.Code != "validation_error" || !strings.Contains(problem.Message, "mime_type") {
						t.Fatalf("expected MIME validation error, got %v", err)
					}
					if len(blobs.data) != 0 {
						t.Fatal("rejected upload stored bytes")
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				var result core.StageAttachmentResult
				if err := json.Unmarshal(out, &result); err != nil {
					t.Fatal(err)
				}
				if result.Attachment.MIMEType != tc.want {
					t.Fatalf("MIME = %q, want %q", result.Attachment.MIMEType, tc.want)
				}
			})
		}
	}
}
