package whatsapp

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/emdioh/megachat/megachatd/internal/proto"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
)

func imageMsg(path, caption string) *waE2E.Message {
	return &waE2E.Message{ImageMessage: &waE2E.ImageMessage{Mimetype: str("image/jpeg"), Caption: str(caption), DirectPath: str(path)}}
}

func withCounting(e *env, delay time.Duration) *countingClient {
	cc := &countingClient{fakeClient: e.fc, delay: delay}
	e.b.lifeMu.Lock()
	e.b.cli = cc
	e.b.lifeMu.Unlock()
	return cc
}

func TestAttachmentDownloadOnDemand(t *testing.T) {
	e := newEnv(t)
	cc := withCounting(e, 0)
	e.fc.download["/img1"] = []byte("JPEGBYTES")
	ev := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "3EB0AB", false, imageMsg("/img1", "foto")), true))
	attID := ev.Payload["attachment_id"].(string)
	if attID != "false_391234567890@c.us_3EB0AB.jpeg" {
		t.Fatalf("attachment id = %q", attID)
	}
	p, ok := e.b.AttachmentPath(ctx, attID)
	if !ok || filepath.Base(p) != attID {
		t.Fatalf("path=%q ok=%v", p, ok)
	}
	if data, _ := os.ReadFile(p); string(data) != "JPEGBYTES" {
		t.Errorf("downloaded data = %q", data)
	}
	if p2, ok := e.b.AttachmentPath(ctx, attID); !ok || p2 != p || cc.downloads() != 1 {
		t.Errorf("second call is served from disk (downloads=%d)", cc.downloads())
	}
	if _, err := os.Stat(filepath.Join(e.cfg.MediaDir(), "."+attID+".part")); err == nil {
		t.Error("temp files must not be left behind")
	}
	// The chunk reader downloads first when the file is missing.
	os.Remove(p)
	chunk, _ := e.b.AttachmentChunk(ctx, attID, 0, 4)
	if string(chunk) != "JPEG" || cc.downloads() != 2 {
		t.Errorf("chunk = %q downloads=%d", chunk, cc.downloads())
	}
}

func TestAttachmentDownloadFailureIsNegativeCached(t *testing.T) {
	e := newEnv(t)
	cc := withCounting(e, 0)
	e.b.lazyNegTTL = time.Minute
	ev := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "3EB0AC", false, imageMsg("/missing", "")), true))
	attID := ev.Payload["attachment_id"].(string)
	if _, ok := e.b.AttachmentPath(ctx, attID); ok {
		t.Fatal("download failure")
	}
	if _, ok := e.b.AttachmentPath(ctx, attID); ok || cc.downloads() != 1 {
		t.Errorf("a recent failure is not retried (downloads=%d)", cc.downloads())
	}
	// Once the negative entry expires the download is retried (and succeeds).
	e.fc.download["/missing"] = []byte("late")
	e.b.mediaMu.Lock()
	for k := range e.b.lazyFailed {
		e.b.lazyFailed[k] = nowFunc().Add(-time.Second)
	}
	e.b.mediaMu.Unlock()
	if _, ok := e.b.AttachmentPath(ctx, attID); !ok {
		t.Error("retry after the negative TTL")
	}
}

func TestAttachmentDownloadsAreCoalesced(t *testing.T) {
	e := newEnv(t)
	cc := withCounting(e, 80*time.Millisecond)
	e.fc.download["/img2"] = []byte("DATA")
	ev := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "3EB0AD", false, imageMsg("/img2", "")), true))
	attID := ev.Payload["attachment_id"].(string)
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := e.b.AttachmentPath(ctx, attID); !ok {
				t.Error("every waiter gets the file")
			}
		}()
	}
	wg.Wait()
	if cc.downloads() != 1 {
		t.Errorf("concurrent requests for one media share one download, got %d", cc.downloads())
	}
}

func TestLazyTokenResolvesPendingMedia(t *testing.T) {
	e := newEnv(t)
	e.fc.download["/img3"] = []byte("PENDING")
	// The row was ingested without an attachment (the Python "pending" media).
	ev := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "3EB0AE", false, imageMsg("/img3", "didascalia")), true))
	msgID := ev.Payload["id"].(string)
	e.b.IngestMessage(ctx, "391234567890@c.us", map[string]any{
		"id": msgID, "text": "", "is_mine": false, "msg_type": "image", "attachment_info": "didascalia"}, 7000)
	tok, ok := lazyMediaToken("391234567890@c.us", msgID)
	if !ok {
		t.Fatal("token")
	}
	p, ok := e.b.AttachmentPath(ctx, tok)
	if !ok {
		t.Fatal("the lazy token must resolve")
	}
	if data, _ := os.ReadFile(p); string(data) != "PENDING" {
		t.Errorf("data = %q", data)
	}
	rows := dbRows(t, e, "391234567890@c.us")
	if len(rows) != 1 || store0(rows[0].AttachmentID) != filepath.Base(p) || store0(rows[0].MediaKind) != "image" {
		t.Fatalf("the row gets its real attachment id and kind: %+v", rows)
	}
	// Garbage tokens and tokens of non-fetchable chats do not resolve.
	if _, ok := e.b.AttachmentPath(ctx, "lazy-wa-garbage"); ok {
		t.Error("garbage token")
	}
	bad, _ := lazyMediaToken("status@broadcast", "x")
	if _, ok := e.b.AttachmentPath(ctx, bad); ok {
		t.Error("broadcast token")
	}
	// A token for an unknown media returns nothing.
	unk, _ := lazyMediaToken("391234567890@c.us", "false_391234567890@c.us_NOPE")
	if _, ok := e.b.AttachmentPath(ctx, unk); ok {
		t.Error("unknown media")
	}
}

func TestMediaPrefetchOnLiveMessage(t *testing.T) {
	e := newEnv(t)
	e.fc.download["/img4"] = []byte("PREFETCH")
	e.b.handleEvent(dmEvent("391234567890", types.DefaultUserServer, "3EB0AF", false, imageMsg("/img4", "")))
	target := filepath.Join(e.cfg.MediaDir(), "false_391234567890@c.us_3EB0AF.jpeg")
	eventually(t, "the prefetch", func() bool { return isFile(target) })
	if len(drain(e.b)) != 1 {
		t.Error("the message event is queued immediately, not after the download")
	}
}

func TestMediaInfoPersistsInDeviceStore(t *testing.T) {
	dir := t.TempDir()
	db, _, _, err := openStore(ctx, filepath.Join(dir, "whatsmeow.db"), quietLogger())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	e := newEnv(t)
	e.b.db = db
	e.fc.download["/img5"] = []byte("PERSISTED")
	ev := one(t, e.b.messageEvents(dmEvent("391234567890", types.DefaultUserServer, "3EB0B0", false, imageMsg("/img5", "")), true))
	// A fresh backend (a restart) still knows how to download the media.
	e2 := newEnv(t)
	e2.b.db = db
	e2.fc.download["/img5"] = []byte("PERSISTED")
	p, ok := e2.b.AttachmentPath(ctx, ev.Payload["attachment_id"].(string))
	if !ok {
		t.Fatal("media info should survive a restart")
	}
	if data, _ := os.ReadFile(p); string(data) != "PERSISTED" {
		t.Errorf("data = %q", data)
	}
	var _ = proto.ProtocolWhatsApp
}
