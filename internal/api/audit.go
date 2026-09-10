package api

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// audit records who changed what (§39). Never stores secrets — callers pass
// business values only.
func audit(ctx context.Context, pool *pgxpool.Pool, actor, action, entity, entityID, oldV, newV string) {
	if len(oldV) > 500 {
		oldV = oldV[:500]
	}
	if len(newV) > 500 {
		newV = newV[:500]
	}
	_, _ = pool.Exec(ctx, `INSERT INTO audit_logs(actor,action,entity,entity_id,old_value,new_value) VALUES($1,$2,$3,$4,$5,$6)`,
		actor, action, entity, entityID, oldV, newV)
}
