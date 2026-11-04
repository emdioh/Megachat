package transcribe

import (
	"net/http"
	"sync"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/store"
)

func newSvc(t *testing.T, base string) (*Service, *memStore) {
	t.Helper()
	st := newMemStore()
	svc := New(Deps{Store: st, Config: newConfig(t, base)})
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.Stop)
	return svc, st
}

func statusOf(st *memStore, id string) string {
	r, _ := st.GetTranscription(ctx(), "whatsapp", id)
	if r == nil {
		return ""
	}
	return r.Status
}

func TestSubmitWorkerPersistsSuccess(t *testing.T) {
	srv := newFakeAPI(t)
	svc, st := newSvc(t, srv.URL)
	audio := writeFile(t, t.TempDir(), "a.mp3", "x")
	if err := svc.Submit(ctx(), "whatsapp", "audio-1", audio); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "ok", func() bool { return statusOf(st, "audio-1") == "ok" })
	r, _ := st.GetTranscription(ctx(), "whatsapp", "audio-1")
	if *r.Text != "testo trascritto" || *r.Model != "gpt-transcribe" || r.Error != nil {
		t.Fatalf("%+v", r)
	}
}

func TestSubmitWorkerPersistsFailure(t *testing.T) {
	srv := newFakeAPI(t)
	srv.handler = func(_ int, _ upload, w http.ResponseWriter) { w.WriteHeader(500); _, _ = w.Write([]byte("boom")) }
	svc, st := newSvc(t, srv.URL)
	audio := writeFile(t, t.TempDir(), "a.mp3", "x")
	_ = svc.Submit(ctx(), "whatsapp", "audio-2", audio)
	waitFor(t, "failed", func() bool { return statusOf(st, "audio-2") == "failed" })
	r, _ := st.GetTranscription(ctx(), "whatsapp", "audio-2")
	if *r.Error != "Errore trascrizione HTTP 500: boom" || r.Text != nil {
		t.Fatalf("%+v", r)
	}
}

func TestDuplicateSubmissionCallsClientOnce(t *testing.T) {
	srv := newFakeAPI(t)
	svc, st := newSvc(t, srv.URL)
	audio := writeFile(t, t.TempDir(), "a.mp3", "x")
	for i := 0; i < 3; i++ {
		_ = svc.Submit(ctx(), "whatsapp", "dup", audio)
	}
	waitFor(t, "ok", func() bool { return statusOf(st, "dup") == "ok" })
	_ = svc.Submit(ctx(), "whatsapp", "dup", audio) // already ok
	if n := len(srv.all()); n != 1 {
		t.Fatalf("%d uploads", n)
	}
}

func TestFailedRecordCanBeResubmitted(t *testing.T) {
	srv := newFakeAPI(t)
	srv.handler = func(n int, _ upload, w http.ResponseWriter) {
		if n == 1 {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	}
	svc, st := newSvc(t, srv.URL)
	audio := writeFile(t, t.TempDir(), "a.mp3", "x")
	_ = svc.Submit(ctx(), "whatsapp", "r", audio)
	waitFor(t, "failed", func() bool { return statusOf(st, "r") == "failed" })
	_ = svc.Submit(ctx(), "whatsapp", "r", audio)
	waitFor(t, "ok", func() bool { return statusOf(st, "r") == "ok" })
}

func TestOnDoneRunsAfterOkAndFailed(t *testing.T) {
	srv := newFakeAPI(t)
	srv.handler = func(n int, _ upload, w http.ResponseWriter) {
		if n == 2 {
			w.WriteHeader(500)
			return
		}
		_, _ = w.Write([]byte(`{"text":"ok"}`))
	}
	svc, _ := newSvc(t, srv.URL)
	var mu sync.Mutex
	var got []string
	svc.SetOnDone(func(p, id string, row store.Transcription) {
		mu.Lock()
		got = append(got, id+":"+row.Status)
		mu.Unlock()
	})
	audio := writeFile(t, t.TempDir(), "a.mp3", "x")
	_ = svc.Submit(ctx(), "whatsapp", "a", audio)
	waitFor(t, "first", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 1 })
	_ = svc.Submit(ctx(), "whatsapp", "b", audio)
	waitFor(t, "second", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) == 2 })
	if got[0] != "a:ok" || got[1] != "b:failed" {
		t.Fatalf("%v", got)
	}
}

func TestNoKeyMeansNoServiceAndReinitKeepsOnDone(t *testing.T) {
	svc, _ := newSvc(t, "")
	if svc.Active() {
		t.Fatal("active without key")
	}
	if err := svc.Submit(ctx(), "whatsapp", "x", "/x"); err != errNotConfigured {
		t.Fatalf("%v", err)
	}
	svc.SetOnDone(func(string, string, store.Transcription) {})
	cfg := svc.deps.Config.(interface{ SetAPIKey(string, string) error })
	_ = cfg.SetAPIKey("openai", "sk-abcdefghijk")
	if err := svc.Reinit(); err != nil || !svc.Active() {
		t.Fatalf("%v active=%v", err, svc.Active())
	}
	if !svc.hasOnDone() {
		t.Fatal("on_done lost on reinit")
	}
}

func TestStopIsIdempotentAndReinitStopsOldWorker(t *testing.T) {
	srv := newFakeAPI(t)
	svc, _ := newSvc(t, srv.URL)
	old := svc.worker()
	if err := svc.Reinit(); err != nil {
		t.Fatal(err)
	}
	svc.wait(old, 0)
	waitFor(t, "old worker exit", func() bool {
		select {
		case <-old.done:
			return true
		default:
			return false
		}
	})
	svc.Stop()
	svc.Stop()
	if svc.Active() {
		t.Fatal("active after stop")
	}
}
