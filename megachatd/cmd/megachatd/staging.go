package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/media"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/rpc"
)

// Attachment policy of the Unix-socket adapter (server/socket_adapter.py,
// mc-7kp): a client names local files by path; the server copies each into
// its upload directory and the operation only ever sees the copies, which are
// deleted when the call ends. The cheap pre-check op runs first so an invalid
// request costs no I/O.
var (
	// stagingChecks maps each staging operation to its side-effect-free pre-check.
	stagingChecks = map[string]string{
		api.OpMessagesSend:      api.OpMessagesSendCheck,
		api.OpGmailMessagesSend: api.OpGmailMessagesSendCheck,
	}
	// plainFileCaps: staging operations whose files are mail attachments, not media.
	plainFileCaps = map[string]int64{api.OpGmailMessagesSend: media.GmailMaxAttachmentBytes}
	// ignoredAttachmentOps have an attachments field that ignores the files.
	ignoredAttachmentOps = map[string]bool{
		api.OpMessagesSendCheck:      true,
		api.OpGmailMessagesSendCheck: true,
	}
)

func invalidRequest() error { return ops.Errorf(api.CodeInvalidRequest, "Invalid request") }

// truthy reports whether a JSON value is truthy the way Python reads it.
func truthy(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", "false", "0", `""`, "[]", "{}":
		return false
	}
	return true
}

// stagingPreCall returns the rpc.PreCall implementing the policy above.
func stagingPreCall(d ops.Dispatcher, uploadDir string) rpc.PreCall {
	return func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, func(), error) {
		check, staging := stagingChecks[method]
		if !staging && !ignoredAttachmentOps[method] {
			return params, nil, nil
		}
		var m map[string]json.RawMessage
		if len(bytes.TrimSpace(params)) == 0 || string(bytes.TrimSpace(params)) == "null" {
			return params, nil, nil
		}
		if err := json.Unmarshal(params, &m); err != nil {
			return params, nil, nil // the dispatcher reports the malformed params
		}
		raw := m["attachments"]
		if ignoredAttachmentOps[method] {
			if truthy(raw) {
				m["attachments"] = json.RawMessage("[]")
				out, err := json.Marshal(m)
				return out, nil, err
			}
			return params, nil, nil
		}
		if !truthy(raw) {
			return params, nil, nil
		}
		var slots []api.AttachmentIn
		if err := json.Unmarshal(raw, &slots); err != nil {
			return nil, nil, invalidRequest()
		}
		// Validate the payload before spending any I/O on files (mc-mbm.10).
		pre := make(map[string]json.RawMessage, len(m)+2)
		for k, v := range m {
			if k != "attachments" {
				pre[k] = v
			}
		}
		slotsOK := true
		for _, s := range slots {
			slotsOK = slotsOK && (s.Path != "" || s.Rejected != nil)
		}
		pre["attachment_count"], _ = json.Marshal(len(slots))
		pre["slots_ok"], _ = json.Marshal(slotsOK)
		preJSON, err := json.Marshal(pre)
		if err != nil {
			return nil, nil, invalidRequest()
		}
		if _, err := d.Call(ctx, check, preJSON); err != nil {
			return nil, nil, err
		}

		var staged []string
		cleanup := func() {
			for _, p := range staged {
				_ = os.Remove(p)
			}
		}
		budget := int64(media.MaxStagedTotalBytes)
		plainCap, plain := plainFileCaps[method]
		out := make([]api.AttachmentIn, 0, len(slots))
		for _, s := range slots {
			if s.Path == "" {
				out = append(out, s) // rejected / empty: the operation decides
				continue
			}
			filename := ""
			if s.Filename != nil {
				filename = *s.Filename
			}
			if filename == "" {
				filename = filepath.Base(s.Path)
			}
			ct := ""
			if s.ContentType != nil {
				ct = *s.ContentType
			}
			limit := budget
			if plain && plainCap < limit {
				limit = plainCap
			}
			copyPath, err := media.StageLocalFile(uploadDir, s.Path, media.StageOptions{
				Filename: filename, ContentType: ct, MaxBytes: limit, NoMediaLimits: plain})
			if err != nil {
				cleanup()
				var ue *media.UploadValidationError
				if errors.As(err, &ue) && ue.Status == 413 {
					return nil, nil, ops.Errorf(api.CodeTooLarge, "Upload too large")
				}
				return nil, nil, invalidRequest()
			}
			staged = append(staged, copyPath)
			if st, serr := os.Stat(copyPath); serr == nil {
				budget -= st.Size()
			}
			fn := filename
			out = append(out, api.AttachmentIn{Path: copyPath, Filename: &fn, ContentType: s.ContentType, Rejected: s.Rejected})
		}
		m["attachments"], _ = json.Marshal(out)
		newParams, err := json.Marshal(m)
		if err != nil {
			cleanup()
			return nil, nil, invalidRequest()
		}
		return newParams, cleanup, nil
	}
}
