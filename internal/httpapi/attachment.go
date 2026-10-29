// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this
// file, You can obtain one at https://mozilla.org/MPL/2.0/.

package httpapi

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"
)

// Images that tools return are often behind short-lived links — a forge-style
// /img/<token> capability URL expires by TTL (see the MCP-server standard §4).
// The model embeds such a URL in its answer as a Markdown image; left as-is, the
// picture is dead once the TTL lapses. imageAttachments fetches each remote image
// while the link is still fresh (right after the turn), stores the bytes in the
// conversation's attachments/, and rewrites the link to the local reference, so
// the image renders for good. It is best-effort: a download that fails leaves the
// original URL, so this step never breaks a message.

// maxAttachmentBytes caps a single downloaded image — generous for a chart or
// photo, small enough to keep memory and disk honest.
const maxAttachmentBytes = 16 << 20 // 16 MiB

// mdRemoteImage matches a Markdown image whose target is an http(s) URL:
// ![alt](https://host/x.png), captured as (prefix)(url)(suffix). Data URIs and
// already-local attachments/ links do not match, so they are left alone.
var mdRemoteImage = regexp.MustCompile(`(!\[[^\]]*\]\()\s*(https?://[^)\s]+)\s*(\))`)

var attachmentClient = &http.Client{Timeout: 20 * time.Second}

// imageAttachments downloads the remote images referenced in content and
// rewrites their links to local attachment refs. Any image that cannot be
// fetched, or is not actually an image, is left untouched.
func (s *Server) imageAttachments(ctx context.Context, convID, content string) string {
	return mdRemoteImage.ReplaceAllStringFunc(content, func(match string) string {
		m := mdRemoteImage.FindStringSubmatch(match)
		ref, err := s.downloadAttachment(ctx, convID, m[2])
		if err != nil {
			log.Printf("attachments: %q: %v", m[2], err)
			return match // leave the original link; never break the message
		}
		return m[1] + ref + m[3]
	})
}

// downloadAttachment fetches url, verifies it is an image, and saves it under a
// random (GUID-style) name in the conversation's attachments/, returning the ref
// ("attachments/<name>"). A random name sidesteps collisions and is path-safe.
func (s *Server) downloadAttachment(ctx context.Context, convID, rawURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := attachmentClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	ext := imageExt(rawURL, resp.Header.Get("Content-Type"))
	if ext == "" {
		return "", fmt.Errorf("not an image (content-type %q)", resp.Header.Get("Content-Type"))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAttachmentBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) > maxAttachmentBytes {
		return "", fmt.Errorf("image exceeds %d bytes", maxAttachmentBytes)
	}
	name, err := newRequestID()
	if err != nil {
		return "", err
	}
	return s.st.SaveAttachment(convID, name+ext, data)
}

// imageExt picks a file extension from the Content-Type, falling back to the URL
// path's extension, limited to the image types we store. "" means "not a
// recognized image", which leaves the link untouched.
func imageExt(rawURL, contentType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])) {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	case "image/svg+xml":
		return ".svg"
	}
	if u, err := url.Parse(rawURL); err == nil {
		switch e := strings.ToLower(path.Ext(u.Path)); e {
		case ".png", ".gif", ".webp", ".svg":
			return e
		case ".jpg", ".jpeg":
			return ".jpg"
		}
	}
	return ""
}

// GET /api/conversations/{id}/attachments/{name} — serve a stored attachment
// (persisted from a tool result, or generated). The store rejects unsafe names.
func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	p, err := s.st.AttachmentPath(r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		http.Error(w, "attachment not found", http.StatusNotFound)
		return
	}
	http.ServeFile(w, r, p)
}
