package support

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/webappsgo/cashp/src/errors"
	"github.com/webappsgo/cashp/src/security"
)

// allowedAttachmentExts is the fixed set of extensions the panel will store.
// An upload whose name does not end in one of these is stored with no
// extension at all, so nothing that lands on disk can be mistaken for a script
// by a web server or by an operator browsing the directory.
var allowedAttachmentExts = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
	".txt":  true,
	".log":  true,
	".json": true,
	".yaml": true,
	".yml":  true,
	".conf": true,
	".pdf":  true,
	".zip":  true,
	".gz":   true,
}

// attachmentExt picks a safe extension for an uploaded file. The caller's name
// is never used as a path component: only this extension survives it.
func attachmentExt(original string) string {
	ext := strings.ToLower(filepath.Ext(original))
	if allowedAttachmentExts[ext] {
		return ext
	}
	return ""
}

// AttachFile stores an upload against a ticket. The stored name is generated
// here and joined to the attachment directory with security.SafeJoin, so a
// crafted filename cannot escape the directory; the caller's own name is kept
// only as a label and is escaped like any other untrusted text on output.
func (s *Service) AttachFile(ctx context.Context, id Identity, ticketID, originalName, contentType string, src io.Reader) (Attachment, error) {
	if !id.Authenticated || id.UserID == 0 {
		return Attachment{}, errors.New(errors.CodeUnauthorized, 401, "Authentication required")
	}
	if s.opts.AttachmentDir == "" {
		return Attachment{}, errors.New(errors.CodeUnavailable, 503, "Attachments are not configured")
	}

	t, err := s.ticketForAttachment(ctx, id, ticketID)
	if err != nil {
		return Attachment{}, err
	}

	maxKB := s.settingInt(ctx, SettingAttachmentMaxKB)
	if maxKB <= 0 {
		maxKB = 10240
	}
	maxBytes := int64(maxKB) * 1024

	stored := newID("att") + attachmentExt(originalName)
	path, err := security.SafeJoin(s.opts.AttachmentDir, stored)
	if err != nil {
		return Attachment{}, errors.New(errors.CodeValidation, 400, "That attachment could not be stored")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return Attachment{}, errors.New(errors.CodeInternal, 500, "That attachment could not be stored")
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return Attachment{}, errors.New(errors.CodeInternal, 500, "That attachment could not be stored")
	}
	// One extra byte is read on purpose: if it arrives, the upload was over
	// the limit and the partial file is removed rather than kept truncated.
	written, copyErr := io.Copy(file, io.LimitReader(src, maxBytes+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > maxBytes {
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			s.logger().Warn("support attachment cleanup failed", "ticket", t.ID)
		}
		if written > maxBytes {
			return Attachment{}, errors.New(errors.CodePayloadTooLarge, 413, "That attachment is too large").
				WithDetails(map[string]any{"max_kilobytes": maxKB})
		}
		return Attachment{}, errors.New(errors.CodeInternal, 500, "That attachment could not be stored")
	}

	a := Attachment{
		ID:           newID("atr"),
		TicketID:     t.ID,
		OrgID:        t.OrgID,
		OriginalName: truncate(clean(originalName), 200),
		StoredName:   stored,
		ContentType:  truncate(clean(contentType), 120),
		SizeBytes:    written,
		UploadedBy:   id.UserID,
		CreatedAt:    s.nowUnix(),
	}
	if a.OriginalName == "" {
		a.OriginalName = "attachment"
	}
	if err := s.store.InsertAttachment(ctx, a); err != nil {
		if rmErr := os.Remove(path); rmErr != nil && !os.IsNotExist(rmErr) {
			s.logger().Warn("support attachment cleanup failed", "ticket", t.ID)
		}
		return Attachment{}, err
	}
	if err := s.audit(ctx, id, AuditEntry{
		OrgID:      t.OrgID,
		Action:     "ticket.attach",
		EntityType: "ticket",
		EntityID:   t.ID,
	}); err != nil {
		return Attachment{}, err
	}
	return a, nil
}

// ticketForAttachment resolves the ticket an upload belongs to under whichever
// role the caller holds, without ever letting one tenant reach another's.
func (s *Service) ticketForAttachment(ctx context.Context, id Identity, ticketID string) (Ticket, error) {
	if _, inMode := s.SupportModeFor(id); inMode {
		if _, err := s.requireAgent(ctx, id); err != nil {
			return Ticket{}, err
		}
		return s.store.TicketAnyOrg(ctx, ticketID)
	}
	t, _, err := s.Ticket(ctx, id, ticketID)
	return t, err
}

// Attachments lists a ticket's files for whoever is allowed to see the ticket.
func (s *Service) Attachments(ctx context.Context, id Identity, ticketID string) ([]Attachment, error) {
	t, err := s.ticketForAttachment(ctx, id, ticketID)
	if err != nil {
		return nil, err
	}
	return s.store.Attachments(ctx, t.OrgID, t.ID)
}

// OpenAttachment returns an attachment's metadata and an open handle to it.
// The path is rebuilt from the stored name through security.SafeJoin every
// time, so even a tampered database row cannot serve a file from outside the
// attachment directory.
func (s *Service) OpenAttachment(ctx context.Context, id Identity, attachmentID string) (Attachment, io.ReadSeekCloser, error) {
	if !id.Authenticated {
		return Attachment{}, nil, errors.New(errors.CodeUnauthorized, 401, "Authentication required")
	}
	if s.opts.AttachmentDir == "" {
		return Attachment{}, nil, notFound("Attachment")
	}

	orgID := id.OrgID
	if _, inMode := s.SupportModeFor(id); inMode {
		if _, err := s.requireAgent(ctx, id); err != nil {
			return Attachment{}, nil, err
		}
		orgID = 0
	}

	var (
		a   Attachment
		err error
	)
	if orgID == 0 {
		a, err = s.store.AttachmentAnyOrg(ctx, attachmentID)
	} else {
		a, err = s.store.Attachment(ctx, orgID, attachmentID)
	}
	if err != nil {
		return Attachment{}, nil, err
	}
	if orgID != 0 {
		if _, _, err := s.Ticket(ctx, id, a.TicketID); err != nil {
			return Attachment{}, nil, notFound("Attachment")
		}
	}

	path, err := security.SafeJoin(s.opts.AttachmentDir, a.StoredName)
	if err != nil {
		return Attachment{}, nil, notFound("Attachment")
	}
	file, err := os.Open(path)
	if err != nil {
		return Attachment{}, nil, notFound("Attachment")
	}
	return a, file, nil
}
