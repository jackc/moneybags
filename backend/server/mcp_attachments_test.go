package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/moneybags/backend/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestMCPBase64JPEGAttachmentRoundTrip(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	client := mcp.NewClient(&mcp.Implementation{Name: "attachment-test", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{f.grant("bags:read bags:write")}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	var original bytes.Buffer
	if err := jpeg.Encode(&original, image.NewRGBA(image.Rect(0, 0, 2, 2)), nil); err != nil {
		t.Fatal(err)
	}
	// Valid base64 can still decode to damaged image bytes. Reject before staging.
	damaged := call("stage_attachment", map[string]any{"request_id": "damaged", "file_name": "receipt.jpg", "mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(original.Bytes()[10:])})
	if !damaged.IsError {
		t.Fatal("accepted a JPEG missing its header")
	}
	message := damaged.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(message, "original file") || !strings.Contains(message, "base64") {
		t.Fatalf("error lacks recovery instructions: %s", message)
	}
	staged := call("stage_attachment", map[string]any{"request_id": "valid", "file_name": "receipt.jpg", "mime_type": "image/jpeg", "data": base64.StdEncoding.EncodeToString(original.Bytes())})
	if staged.IsError {
		t.Fatalf("staging: %+v", staged)
	}
	var upload core.StageAttachmentResult
	if err := json.Unmarshal([]byte(staged.Content[0].(*mcp.TextContent).Text), &upload); err != nil {
		t.Fatal(err)
	}
	if upload.Attachment.MIMEType != "image/jpeg" || upload.Attachment.Size != int64(original.Len()) {
		t.Fatalf("wrong staged metadata: %+v", upload.Attachment)
	}
	bag := f.call("create_bag", map[string]any{"request_id": "bag", "name": "Groceries", "initial_amount_cents": 0}, 200)
	entry := call("create_entry", map[string]any{"request_id": "expense", "bag_id": bag["id"], "amount_cents": -4605, "attachment_upload_ids": []string{upload.UploadID}})
	if entry.IsError {
		t.Fatalf("create entry: %+v", entry)
	}
	got := call("get_attachment", map[string]any{"attachment_id": upload.Attachment.ID})
	if got.IsError || len(got.Content) != 2 {
		t.Fatalf("get attachment: %+v", got)
	}
	picture, ok := got.Content[1].(*mcp.ImageContent)
	if !ok || picture.MIMEType != "image/jpeg" || !bytes.Equal(picture.Data, original.Bytes()) {
		t.Fatalf("attachment did not retain JPEG MIME and exact original bytes: %T", got.Content[1])
	}
}
