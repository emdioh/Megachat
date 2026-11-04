package core

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Port of protocols/media_prune.py: best-effort removal of media files that
// the message cache no longer references.

// DefaultGrace is the minimum age of an orphan before it is removed.
const DefaultGrace = time.Hour

// MediaScope is one directory of media files owned by a protocol.
type MediaScope struct {
	Protocol string
	Label    string
	Dir      string
}

// OrphanFile is an unreferenced file.
type OrphanFile struct {
	Path   string
	Size   int64
	Reason string
}

// ScopeReport is the outcome for one scope.
type ScopeReport struct {
	Scope             string
	Dir               string
	FilesScanned      int
	RefsCollected     int
	OrphansFound      int
	OrphansDeleted    int
	OrphansDryRun     []OrphanFile
	BytesFreed        int64
	SkippedGrace      int
	Errors            int
	SkippedDirMissing bool
	SkippedDBError    bool
}

// MediaPruneReport is the outcome of PruneMedia.
type MediaPruneReport struct{ Scopes []ScopeReport }

// TotalOrphans sums the orphans found.
func (r MediaPruneReport) TotalOrphans() int {
	n := 0
	for _, s := range r.Scopes {
		n += s.OrphansFound
	}
	return n
}

// TotalBytes sums freed bytes plus dry-run sizes.
func (r MediaPruneReport) TotalBytes() int64 {
	var n int64
	for _, s := range r.Scopes {
		n += s.BytesFreed
		for _, o := range s.OrphansDryRun {
			n += o.Size
		}
	}
	return n
}

// MediaPruneOptions parameterises PruneMedia.
type MediaPruneOptions struct {
	Protocols []string // nil = all scopes
	Scopes    []MediaScope
	Grace     time.Duration // 0 = DefaultGrace; negative = no grace
	DryRun    bool
	Now       time.Time
}

// ResolveProtected maps raw DB references to the basenames protected in scope.
func ResolveProtected(scope MediaScope, refs []string, diskNames []string) map[string]struct{} {
	protected := map[string]struct{}{}
	for _, ref := range refs {
		if scope.Label == api.ProtocolTelegram && strings.HasPrefix(ref, "tgref:") {
			i := strings.LastIndex(ref, ":")
			if i < 0 {
				continue
			}
			rest := ref[:i]
			j := strings.LastIndex(rest, ":")
			if j < 0 {
				continue
			}
			prefix, chat, msg := rest[:j], rest[j+1:], ref[i+1:]
			if prefix != "tgref" || chat == "" || msg == "" {
				continue
			}
			fp := chat + "-" + msg + "-"
			for _, n := range diskNames {
				if strings.HasPrefix(n, fp) {
					protected[n] = struct{}{}
				}
			}
			continue
		}
		base := filepath.Base(ref)
		if base == "." || base == "/" {
			base = ""
		}
		if scope.Label == api.ProtocolWhatsApp && base == "" {
			base = ref
		}
		protected[base] = struct{}{}
	}
	return protected
}

// DefaultMediaScopes resolves the scope registry from the configuration.
func (c *Core) DefaultMediaScopes() []MediaScope {
	return []MediaScope{
		{api.ProtocolSignal, api.ProtocolSignal, c.cfg.SignalCLIAttachmentsDir()},
		{api.ProtocolWhatsApp, api.ProtocolWhatsApp, c.cfg.MediaDir()},
		{api.ProtocolTelegram, api.ProtocolTelegram, c.cfg.TelegramMediaDir()},
		{api.ProtocolSignal, "quote-thumbs", c.cfg.QuoteThumbsDir()},
	}
}

// PruneMedia scans the media scopes and removes (unless DryRun) the files no
// message references and that are older than the grace period.
func PruneMedia(ctx context.Context, st *store.Store, opt MediaPruneOptions, log *slog.Logger) MediaPruneReport {
	var report MediaPruneReport
	grace := opt.Grace
	if grace == 0 {
		grace = DefaultGrace
	}
	if grace < 0 {
		grace = 0
	}
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	scopes := opt.Scopes
	if opt.Protocols != nil {
		sel := map[string]bool{}
		for _, p := range opt.Protocols {
			sel[p] = true
		}
		var kept []MediaScope
		for _, s := range scopes {
			if (s.Label == "quote-thumbs" && sel[api.ProtocolSignal]) ||
				(s.Label != "quote-thumbs" && sel[s.Protocol]) {
				kept = append(kept, s)
			}
		}
		scopes = kept
	}
	for _, scope := range scopes {
		sr := ScopeReport{Scope: scope.Label, Dir: scope.Dir}
		func() {
			if _, err := os.Stat(scope.Dir); err != nil {
				sr.SkippedDirMissing = true
				return
			}
			refsByProto, err := st.MediaReferences(ctx, []string{scope.Protocol})
			if err != nil {
				sr.SkippedDBError = true
				sr.Errors++
				log.Debug("media prune: references unavailable", "err", err)
				return
			}
			var refs []string
			for r := range refsByProto[scope.Protocol] {
				refs = append(refs, r)
			}
			sr.RefsCollected = len(refs)
			entries, err := os.ReadDir(scope.Dir)
			if err != nil {
				sr.Errors++
				return
			}
			var files []string
			for _, e := range entries {
				info, err := os.Stat(filepath.Join(scope.Dir, e.Name()))
				if err != nil {
					sr.Errors++
					continue
				}
				if info.Mode().IsRegular() {
					files = append(files, e.Name())
				}
			}
			sr.FilesScanned = len(files)
			protected := ResolveProtected(scope, refs, files)
			var orphans []OrphanFile
			for _, name := range files {
				if _, ok := protected[name]; ok {
					continue
				}
				p := filepath.Join(scope.Dir, name)
				info, err := os.Stat(p)
				if err != nil {
					sr.Errors++
					continue
				}
				if now.Sub(info.ModTime()) < grace {
					sr.SkippedGrace++
					continue
				}
				orphans = append(orphans, OrphanFile{p, info.Size(), "unreferenced"})
			}
			sr.OrphansFound = len(orphans)
			if opt.DryRun {
				sr.OrphansDryRun = orphans
				return
			}
			for _, o := range orphans {
				if err := os.Remove(o.Path); err != nil && !os.IsNotExist(err) {
					sr.Errors++
					continue
				}
				sr.OrphansDeleted++
				sr.BytesFreed += o.Size
			}
		}()
		report.Scopes = append(report.Scopes, sr)
	}
	return report
}
