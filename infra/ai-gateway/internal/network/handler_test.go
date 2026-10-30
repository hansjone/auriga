package network

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func testID(epoch uint64) NodeIdentity {
	return NodeIdentity{
		TenantID:    "t1",
		Realm:       "2",
		NodeID:      "64",
		Incarnation: "inc-64-1",
		Epoch:       epoch,
	}
}

func TestDecodeFetchAndReceiptFixturesShape(t *testing.T) {
	fetchRaw := []byte(`{
		"app_version":"1.0","type":"fetch","request_id":"req-fetch-0001","epoch":"42",
		"resume_cursor":"1200","continuation":null,"max_items":128,"max_bytes":65536,
		"held_ids":["rcp-0001"]
	}`)
	msg, err := DecodeUpstream(fetchRaw)
	if err != nil {
		t.Fatal(err)
	}
	fr := msg.(*FetchRequest)
	if fr.Epoch != 42 || fr.ResumeCursor != 1200 || fr.MaxItems != 128 {
		t.Fatalf("fetch decode %+v", fr)
	}

	bad := []byte(`{"app_version":"1.0","type":"fetch","request_id":"r","epoch":"42","resume_cursor":"0","continuation":null,"max_items":999,"max_bytes":65536}`)
	if _, err := DecodeUpstream(bad); err == nil || err.Code != CodeProtocolError {
		t.Fatalf("want protocol_error for max_items, got %v", err)
	}

	receiptRaw := []byte(`{
		"app_version":"1.0","type":"receipt","request_id":"req-up-0001","operation_id":"op-1",
		"epoch":"42","delivery_id":"dlv-1","mail_seq":"1",
		"results":[{
			"recipient_id":"rcp-a","target":{"version":1,"realm":"2","type":"session","node":"64","local":"2"},
			"message_digest":"sha256:aa","host_request_id":"rcp-a","status":"host_accepted",
			"expected_state_version":"1","accepted_by":"session","acceptance_mode":"normal",
			"execution_status":"not_applicable","evidence_digest":"sha256:bb"
		}]
	}`)
	msg, err = DecodeUpstream(receiptRaw)
	if err != nil {
		t.Fatal(err)
	}
	rr := msg.(*ReceiptRequest)
	if rr.Results[0].TransportStatus != "host_accepted" {
		t.Fatalf("status map %+v", rr.Results[0])
	}
}

func TestHeadReturnsRoute(t *testing.T) {
	box := NewMemoryMailbox(7)
	box.Seed("d1", "m1", "hi", "message", []RecipientView{{RecipientID: "r1", Local: "1"}})
	h := &Handler{Mailbox: box}
	route, err := h.Head(context.Background(), testID(7))
	if err != nil {
		t.Fatal(err)
	}
	if route.HeadSeq != 1 || route.CommittedCursor != 0 || route.ConnectionEpoch != 7 {
		t.Fatalf("route %+v", route)
	}
	raw, encErr := EncodeHead(route, nil)
	if encErr != nil {
		t.Fatal(encErr)
	}
	var tip map[string]any
	if json.Unmarshal(raw, &tip) != nil || tip["type"] != "head" {
		t.Fatalf("head json %s", raw)
	}
}

func TestFetchAndIdempotentReceipt(t *testing.T) {
	box := NewMemoryMailbox(42)
	seq := box.Seed("dlv-1", "msg-1", "body", "message", []RecipientView{
		{RecipientID: "rcp-a", Local: "2"},
		{RecipientID: "rcp-b", Local: "3"},
	})
	h := &Handler{Mailbox: box}
	id := testID(42)

	fetchRaw := []byte(`{
		"app_version":"1.0","type":"fetch","request_id":"req-f1","epoch":"42",
		"resume_cursor":"0","continuation":null,"max_items":10,"max_bytes":4096
	}`)
	out, err := h.HandleUpstream(context.Background(), id, fetchRaw)
	if err != nil {
		t.Fatal(err)
	}
	var fr map[string]any
	if json.Unmarshal(out, &fr) != nil || fr["type"] != "fetch_result" {
		t.Fatalf("fetch_result %s", out)
	}
	page := fr["page"].(map[string]any)
	if page["head_seq"] != "1" || page["committed_cursor"] != "0" {
		t.Fatalf("page %+v", page)
	}

	receiptTpl := `{
		"app_version":"1.0","type":"receipt","request_id":"req-r1","operation_id":"op-1",
		"epoch":"42","delivery_id":"dlv-1","mail_seq":"` + u64str(seq) + `",
		"results":[
			{"recipient_id":"rcp-a","target":{"version":1,"realm":"2","type":"session","node":"64","local":"2"},
			 "message_digest":"sha256:aa","host_request_id":"rcp-a","status":"host_accepted",
			 "expected_state_version":"1","accepted_by":"session","acceptance_mode":"normal",
			 "execution_status":"not_applicable","evidence_digest":"sha256:bb"},
			{"recipient_id":"rcp-b","target":{"version":1,"realm":"2","type":"session","node":"64","local":"3"},
			 "message_digest":"sha256:aa","host_request_id":"rcp-b","status":"rejected",
			 "code":"network.target_unavailable","expected_state_version":"1"}
		]
	}`
	out, err = h.HandleUpstream(context.Background(), id, []byte(receiptTpl))
	if err != nil {
		t.Fatal(err)
	}
	var rc map[string]any
	if json.Unmarshal(out, &rc) != nil || rc["type"] != "receipt_committed" {
		t.Fatalf("committed %s", out)
	}
	if rc["committed_cursor"] != "1" || rc["mail_item_closed"] != true {
		t.Fatalf("commit %+v", rc)
	}

	// Idempotent replay
	out2, err := h.HandleUpstream(context.Background(), id, []byte(receiptTpl))
	if err != nil {
		t.Fatal(err)
	}
	var rc2 map[string]any
	_ = json.Unmarshal(out2, &rc2)
	if rc2["type"] != "receipt_committed" || rc2["committed_cursor"] != "1" {
		t.Fatalf("idempotent %s", out2)
	}
}

func TestStaleEpoch(t *testing.T) {
	box := NewMemoryMailbox(42)
	box.Seed("dlv-1", "msg-1", "x", "message", []RecipientView{{RecipientID: "r1", Local: "1"}})
	h := &Handler{Mailbox: box}
	raw := []byte(`{
		"app_version":"1.0","type":"fetch","request_id":"req-stale","epoch":"41",
		"resume_cursor":"0","continuation":null,"max_items":1,"max_bytes":4096
	}`)
	out, err := h.HandleUpstream(context.Background(), testID(42), raw)
	if err != nil {
		t.Fatal(err)
	}
	var e map[string]any
	_ = json.Unmarshal(out, &e)
	if e["type"] != "error" || e["code"] != CodeStaleEpoch {
		t.Fatalf("want stale_epoch, got %s", out)
	}
}

func TestFetchLimitClamp(t *testing.T) {
	box := NewMemoryMailbox(1)
	for i := 0; i < 5; i++ {
		box.Seed("d"+u64str(uint64(i)), "m"+u64str(uint64(i)), "b", "message",
			[]RecipientView{{RecipientID: "r", Local: "1"}})
	}
	h := &Handler{Mailbox: box}
	// max_items=3 within contract
	raw := []byte(`{
		"app_version":"1.0","type":"fetch","request_id":"req-lim","epoch":"1",
		"resume_cursor":"0","continuation":null,"max_items":3,"max_bytes":4096
	}`)
	out, err := h.HandleUpstream(context.Background(), testID(1), raw)
	if err != nil {
		t.Fatal(err)
	}
	var fr map[string]any
	_ = json.Unmarshal(out, &fr)
	items := fr["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("want 3 items, got %d", len(items))
	}
	// Over contract max rejected at decode
	rawBad := []byte(`{
		"app_version":"1.0","type":"fetch","request_id":"req-lim2","epoch":"1",
		"resume_cursor":"0","continuation":null,"max_items":129,"max_bytes":4096
	}`)
	out, err = h.HandleUpstream(context.Background(), testID(1), rawBad)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), CodeProtocolError) {
		t.Fatalf("want protocol_error, got %s", out)
	}
}

func TestEmptyReceiptResultsProtocolError(t *testing.T) {
	raw := []byte(`{
		"app_version":"1.0","type":"receipt","request_id":"req-q","operation_id":"op",
		"epoch":"1","delivery_id":"d","mail_seq":"1","results":[]
	}`)
	_, err := DecodeUpstream(raw)
	if err == nil || err.Code != CodeProtocolError {
		t.Fatalf("empty results: %v", err)
	}
}
