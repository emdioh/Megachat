package ops

import (
	"encoding/json"
	"testing"

	"github.com/emdioh/megachat/megachatd/internal/api"
)

func TestValidateRequestCollectsEveryField(t *testing.T) {
	_, err := validateRequest(api.OpAutotranscribeSetChat, json.RawMessage(`{"protocol":"zzz","contact_id":5}`))
	if err == nil {
		t.Fatal("want invalid_request")
	}
	d := err.Details.([]api.ValidationDetail)
	if len(d) != 3 || d[0].Type != "literal_error" || d[1].Type != "string_type" || d[2].Type != "missing" {
		t.Fatalf("%+v", d)
	}
}

func TestValidateRequestStrippedAndInt(t *testing.T) {
	p, err := validateRequest(api.OpChatsMarkRead, json.RawMessage(`{"protocol":"signal","contact_id":5}`))
	if err != nil || string(p) != `{"contact_id":"5","protocol":"signal"}` {
		t.Fatalf("%s %v", p, err)
	}
	_, err = validateRequest(api.OpGmailMessagesGet, json.RawMessage(`{"uid":"x"}`))
	if err == nil || err.Details.([]api.ValidationDetail)[0].Type != "int_parsing" {
		t.Fatalf("%v", err)
	}
	if _, err = validateRequest(api.OpGmailMessagesGet, json.RawMessage(`{"uid":"7"}`)); err != nil {
		t.Fatal(err)
	}
}
