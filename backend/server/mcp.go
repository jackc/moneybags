package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/jackc/moneybags/backend/core"
	"github.com/jackc/moneybags/backend/domain"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (a *API) mcpHandler() http.Handler {
	srv := mcp.NewServer(&mcp.Implementation{Name: "moneybags", Title: "Money Bags", Version: "0.1.0"}, &mcp.ServerOptions{Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}}})
	for _, info := range a.core.Actions() {
		if !info.MCP {
			continue
		}
		schema := info.InputSchema
		if len(schema) == 0 {
			schema = schemaFor(reflect.TypeOf(info.NewParams()))
		}
		description := info.Description
		if description == "" {
			description = strings.ReplaceAll(info.Name, "_", " ") + " for your shared family."
		}
		if info.Mutation && schemaHasProperty(schema, "request_id") {
			description += " Reuse the same request_id and inputs when retrying a call. Different entries require distinct request IDs."
		}
		if info.Name == "create_entry" {
			description += " amount_cents is a signed integer in USD cents: spending is negative, funding/refunds positive, and zero adds a chronological note. Each entry affects exactly one bag. Notes and files never determine amounts. Ask the user if the bag or amount is unclear."
		}
		destructive := info.Mutation && (strings.HasPrefix(info.Name, "delete_") || strings.HasPrefix(info.Name, "revoke_") || strings.HasPrefix(info.Name, "update_") || info.Name == "archive_bag")
		openWorld := info.Name == "create_entry" || info.Name == "update_entry" || info.Name == "stage_attachment"
		meta := mcp.Meta{}
		if schemaHasProperty(schema, "files") {
			// https://developers.openai.com/plugins/reference#define-file-inputs
			properties := schema["properties"].(map[string]any)
			properties["files"] = map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{"download_url": map[string]any{"type": "string"}, "file_id": map[string]any{"type": "string"}, "mime_type": map[string]any{"type": "string"}, "file_name": map[string]any{"type": "string"}}, "required": []string{"download_url", "file_id"}, "additionalProperties": false}}
			meta["openai/fileParams"] = []string{"files"}
		}
		srv.AddTool(&mcp.Tool{Name: info.Name, Description: description, InputSchema: schema, Meta: meta, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: !info.Mutation, DestructiveHint: &destructive, IdempotentHint: true, OpenWorldHint: &openWorld}}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			started := time.Now()
			out, e := a.core.InvokeJSON(ctx, info.Name, req.Params.Arguments)
			status := "ok"
			if e != nil {
				status = "error"
			}
			requestID, _ := ctx.Value(requestIDContextKey{}).(string)
			a.config.Logger.Info("action", "request_id", requestID, "action", info.Name, "source", "mcp", "duration_ms", time.Since(started).Milliseconds(), "status", status)
			if e != nil {
				var problem map[string]any
				if known, ok := e.(*core.Error); ok {
					problem = map[string]any{"error": known}
				} else {
					problem = map[string]any{"error": core.E("internal_error", "The operation could not be completed")}
				}
				b, _ := json.Marshal(problem)
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, StructuredContent: problem}, nil
			}
			var result map[string]any
			if e = json.Unmarshal(out, &result); e != nil {
				return nil, e
			}
			if info.Name == "get_attachment" {
				var file struct {
					Data       []byte            `json:"data"`
					Attachment domain.Attachment `json:"attachment"`
					MIMEType   string            `json:"mime_type"`
					ID         string            `json:"id"`
				}
				if e = json.Unmarshal(out, &file); e != nil {
					return nil, e
				}
				delete(result, "data")
				metadata, _ := json.Marshal(result)
				mimeType := file.MIMEType
				if mimeType == "" {
					mimeType = file.Attachment.MIMEType
				}
				content := []mcp.Content{&mcp.TextContent{Text: string(metadata)}}
				if mimeType == "image/png" || mimeType == "image/jpeg" || mimeType == "image/webp" {
					content = append(content, &mcp.ImageContent{Data: file.Data, MIMEType: mimeType})
				} else {
					content = append(content, &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "moneybags://attachments/" + file.ID, MIMEType: mimeType, Blob: file.Data}})
				}
				return &mcp.CallToolResult{Content: content, StructuredContent: result}, nil
			}
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(out)}}, StructuredContent: result}, nil
		})
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return srv }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true, DisableLocalhostProtection: true})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != a.config.Origin {
			a.fail(w, core.E("permission_denied", "Request origin is not allowed"))
			return
		}
		auth, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if !ok || !strings.EqualFold(auth, "Bearer") || strings.TrimSpace(token) == "" {
			a.bearerChallenge(w)
			return
		}
		raw, _ := json.Marshal(core.TokenParams{Token: strings.TrimSpace(token)})
		out, e := a.core.InvokeJSON(r.Context(), "authenticate_oauth_token", raw)
		if e != nil {
			a.bearerChallenge(w)
			return
		}
		var p core.Principal
		if json.Unmarshal(out, &p) != nil {
			a.bearerChallenge(w)
			return
		}
		ctx := core.WithPrincipal(r.Context(), p)
		if !a.limit(w, r, ctx, false) {
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 30<<20)
		handler.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *API) bearerChallenge(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer resource_metadata=%q, scope="bags:read"`, a.config.Origin+"/.well-known/oauth-protected-resource"))
	writeJSON(w, 401, map[string]string{"error": "invalid_token"})
}

// Tool schemas come from the exact parameter types used by core. Validation
// still runs in core for all transports, including required pointer amounts.
func schemaFor(t reflect.Type) map[string]any {
	if t == nil {
		return map[string]any{"type": "object", "properties": map[string]any{}}
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		props := map[string]any{}
		required := []string{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if !f.IsExported() {
				continue
			}
			tag := f.Tag.Get("json")
			name, opts, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if f.Anonymous && name == "" {
				embedded := schemaFor(f.Type)
				if p, ok := embedded["properties"].(map[string]any); ok {
					for k, v := range p {
						props[k] = v
					}
				}
				if req, ok := embedded["required"].([]string); ok {
					required = append(required, req...)
				}
				continue
			}
			if name == "" {
				name = f.Name
			}
			s := schemaFor(f.Type)
			switch name {
			case "amount_cents", "initial_amount_cents":
				s["description"] = "Signed USD cents. Zero is valid; never send decimal dollars."
				s["minimum"] = -domain.MaxSafeCents
				s["maximum"] = domain.MaxSafeCents
			case "request_id":
				s["description"] = "Client-generated request identifier. Reuse on retries of this same operation."
			case "notes":
				s["description"] = "Optional Markdown source; receipt tables are descriptive and do not determine financial amounts."
			case "file_id":
				s["description"] = "Stable client file identity, unchanged when a temporary download URL is refreshed."
			case "download_url":
				s["description"] = "Temporary public HTTPS download URL; do not provide local or private addresses."
			}
			props[name] = s
			if !strings.Contains(opts, "omitempty") {
				required = append(required, name)
			}
		}
		result := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
		if len(required) > 0 {
			result["required"] = required
		}
		return result
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}
		}
		return map[string]any{"type": "array", "items": schemaFor(t.Elem())}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	default:
		return map[string]any{"type": "string"}
	}
}

func schemaHasProperty(schema map[string]any, name string) bool {
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = properties[name]
	return ok
}
