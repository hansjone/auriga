package bifrostbackend

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
)

// A durable claim is committed before dispatch. A crash leaves a blocked retry,
// never a duplicate mutation. Successful replies (including issued VKs) are
// encrypted at rest and replayed only for the same tenant, key and request.
func (b *Backend) idempotentManagement(w http.ResponseWriter, r *http.Request, tenant string) {
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || len(keys[0]) < 1 || len(keys[0]) > 128 {
		reject(w, invalid)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	if err != nil {
		reject(w, invalid)
		return
	}
	sum := sha256.Sum256([]byte(keys[0]))
	id := hex.EncodeToString(sum[:])
	fingerprint := sha256.Sum256(append([]byte(r.Method+" "+r.URL.RequestURI()+"\n"), raw...))
	var replay []byte
	owned := false
	err = b.db.Transaction(r.Context(), tenant, func(tx pgx.Tx) error {
		tag, e := tx.Exec(r.Context(), `INSERT INTO ai.relay_admin_request(tenant_id,id,fingerprint) VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, tenant, id, fingerprint[:])
		if e != nil {
			return e
		}
		owned = tag.RowsAffected() == 1
		if owned {
			return nil
		}
		var previous []byte
		if e = tx.QueryRow(r.Context(), `SELECT fingerprint,response FROM ai.relay_admin_request WHERE tenant_id=$1 AND id=$2`, tenant, id).Scan(&previous, &replay); e != nil {
			return e
		}
		if !bytes.Equal(previous, fingerprint[:]) {
			return fault("common.conflict")
		}
		if len(replay) == 0 {
			return fault("ai.request_in_progress")
		}
		return nil
	})
	if err != nil {
		reject(w, err)
		return
	}
	if !owned {
		plain, e := b.decrypt(tenant, id, replay)
		if e != nil {
			reject(w, e)
			return
		}
		var result managementReply
		if json.Unmarshal([]byte(plain), &result) != nil {
			reject(w, denied)
			return
		}
		result.send(w)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(raw))
	result := managementReply{Status: 200, Headers: make(http.Header)}
	if err = b.management(&result, r, tenant); err != nil {
		reject(&result, err)
	}
	encoded, err := json.Marshal(result)
	if err == nil {
		replay, err = b.encrypt(tenant, id, string(encoded))
	}
	if err == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err = b.db.Transaction(ctx, tenant, func(tx pgx.Tx) error {
			_, e := tx.Exec(ctx, `UPDATE ai.relay_admin_request SET response=$3 WHERE tenant_id=$1 AND id=$2`, tenant, id, replay)
			return e
		})
	}
	if err != nil {
		reject(w, err)
		return
	}
	result.send(w)
}

type managementReply struct {
	Status  int
	Headers http.Header
	Body    []byte
}

func (r *managementReply) Header() http.Header    { return r.Headers }
func (r *managementReply) WriteHeader(status int) { r.Status = status }
func (r *managementReply) Write(raw []byte) (int, error) {
	r.Body = append(r.Body, raw...)
	return len(raw), nil
}
func (r managementReply) send(w http.ResponseWriter) {
	for key, values := range r.Headers {
		w.Header()[key] = values
	}
	w.WriteHeader(r.Status)
	_, _ = w.Write(r.Body)
}
