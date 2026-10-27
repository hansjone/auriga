package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	iam "github.com/hansjone/auriga/infra/gen/auriga/iam/v1"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

var snapshotTables = map[string][]string{
	"iam":    {"network_connection_anchor", "network_source_anchor", "network_source_chain", "network_source", "network_source_operation"},
	"collab": {"network_node", "network_local_registration", "network_source_lineage", "network_source_operation", "network_source_outbox", "network_source_binding"},
}

// Each snapshot uses the real runtime role, READ ONLY transaction and tenant
// RLS. Only source-specific tables are allowlisted. No credential table/export.
func snapshot(parent context.Context, port uint16, tenant string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	out := map[string]any{}
	for schema, tables := range snapshotTables {
		cfg, err := pgx.ParseConfig("")
		if err != nil {
			return nil, err
		}
		cfg.Host, cfg.Port, cfg.Database, cfg.User, cfg.Password = "127.0.0.1", port, "bridge", "bridge_"+schema+"_app", ""
		cfg.TLSConfig = nil
		cfg.Fallbacks = nil
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return nil, err
		}
		err = func() error {
			tx, e := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
			if e != nil {
				return e
			}
			defer func() { _ = tx.Rollback(ctx) }()
			if _, e = tx.Exec(ctx, "SELECT set_config('auriga.tenant_id',$1,true)", tenant); e != nil {
				return e
			}
			for _, table := range tables {
				rows, e := tx.Query(ctx, "SELECT row_to_json(t)::text FROM "+schema+"."+table+" t ORDER BY id")
				if e != nil {
					return e
				}
				values := []map[string]any{}
				for rows.Next() {
					var raw string
					if e = rows.Scan(&raw); e != nil {
						rows.Close()
						return e
					}
					var value map[string]any
					decoder := json.NewDecoder(strings.NewReader(raw))
					decoder.UseNumber()
					if e = decoder.Decode(&value); e != nil {
						rows.Close()
						return e
					}
					for field, number := range value {
						if exact, ok := number.(json.Number); ok {
							value[field] = exact.String()
						}
					}
					if e = decodeFacts(schema, table, value); e != nil {
						rows.Close()
						return e
					}
					values = append(values, value)
				}
				e = rows.Err()
				rows.Close()
				if e != nil {
					return e
				}
				out[schema+"."+table] = values
			}
			return tx.Commit(ctx)
		}()
		closeCtx, stop := context.WithTimeout(context.Background(), time.Second)
		_ = conn.Close(closeCtx)
		stop()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func decodeFacts(schema, table string, row map[string]any) error {
	fields := map[string]proto.Message{}
	if schema == "iam" {
		switch table {
		case "network_connection_anchor", "network_source_anchor":
			fields["connection"] = &iam.NetworkAuthorityConnection{}
		case "network_source":
			fields["fact"] = &iam.NetworkAgentSource{}
		case "network_source_chain":
			fields["registration"] = &iam.NetworkAgentSourceRegistration{}
		case "network_source_operation":
			if row["method"] == "Register" {
				fields["request"] = &iam.RegisterAgentSourceRequest{}
				fields["response"] = &iam.RegisterAgentSourceResponse{}
			} else {
				fields["request"] = &iam.RevokeAgentSourceRequest{}
				fields["response"] = &iam.RevokeAgentSourceResponse{}
			}
		}
	} else if table == "network_source_operation" {
		fields["original_connection"] = &iam.NetworkAuthorityConnection{}
		if row["method"] == "Register" {
			fields["iam_request"] = &iam.RegisterAgentSourceRequest{}
			fields["iam_response"] = &iam.RegisterAgentSourceResponse{}
		} else {
			fields["iam_request"] = &iam.RevokeAgentSourceRequest{}
			fields["iam_response"] = &iam.RevokeAgentSourceResponse{}
		}
	}
	for name, message := range fields {
		if row[name] == nil {
			continue
		}
		raw, ok := row[name].(string)
		if !ok || !strings.HasPrefix(raw, `\x`) {
			return errors.New("invalid persisted protobuf bytes")
		}
		data, err := hex.DecodeString(raw[2:])
		if err != nil {
			return err
		}
		if err = proto.Unmarshal(data, message); err != nil {
			return err
		}
		data, err = (protojson.MarshalOptions{UseProtoNames: true, EmitDefaultValues: true}).Marshal(message)
		if err != nil {
			return err
		}
		row[name+"_decoded"] = json.RawMessage(data)
	}
	return nil
}
