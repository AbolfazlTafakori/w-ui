package reconciler

// Writing a flush's usage a few hundred rows to a statement.
//
// A flush on a busy server carries every customer who moved and every file
// that did. Written a row at a time -- a customer's row, a look for their
// sample, the sample, each file -- twenty thousand customers online were
// eighty thousand statements every five seconds, each built and compiled
// afresh, holding the database while the loop waited for it: on a two-core
// server the loop fell to a pass every six seconds. Here each kind of row is
// written in chunks, one statement a chunk, to the same values as before:
// the same columns move by the same amounts, the same samples are found or
// made, the same reseller totals follow. Only how many statements carry it
// changed.
//
// The statements are the same on SQLite and PostgreSQL. A value in VALUES
// has no type on PostgreSQL, so numbers are cast to BIGINT there and on
// SQLite alike; a time is cast on PostgreSQL only, since on SQLite a cast to
// a time type turns the text it is stored as into a number.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/abolfazl/w-ui/internal/database/model"
)

// writeChunk is how many rows one statement carries: well inside both
// engines' limits on bound values, at four or five values a row.
const writeChunk = 400

func sortedIDs[T any](m map[uint]T) []uint {
	ids := make([]uint, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	// In one order, so two writers on PostgreSQL take row locks alike.
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func chunks(ids []uint) [][]uint {
	var out [][]uint
	for len(ids) > writeChunk {
		out = append(out, ids[:writeChunk])
		ids = ids[writeChunk:]
	}
	if len(ids) > 0 {
		out = append(out, ids)
	}
	return out
}

// addToRows adds to columns of table's rows, by id, in one statement for
// the lot: rows holds each id's amounts, one for each column, in order.
func addToRows(tx *gorm.DB, table string, columns []string, ids []uint, rows func(id uint) []uint64) error {
	if len(ids) == 0 {
		return nil
	}
	cell := "CAST(? AS BIGINT)"
	var vals strings.Builder
	args := make([]any, 0, len(ids)*(len(columns)+1))
	for i, id := range ids {
		if i > 0 {
			vals.WriteString(", ")
		}
		vals.WriteString("(" + cell)
		args = append(args, int64(id))
		for _, n := range rows(id) {
			vals.WriteString(", " + cell)
			args = append(args, int64(n))
		}
		vals.WriteString(")")
	}
	sets := make([]string, len(columns))
	for i, c := range columns {
		sets[i] = fmt.Sprintf("%s = %s.%s + v.column%d", c, table, c, i+2)
	}
	q := fmt.Sprintf("UPDATE %s SET %s FROM (VALUES %s) AS v WHERE %s.id = v.column1",
		table, strings.Join(sets, ", "), vals.String(), table)
	return tx.Exec(q, args...).Error
}

// writeFlush is a flush's writes, inside its transaction.
func writeFlush(tx *gorm.DB, usage, devices map[uint]usageDelta, liveness map[uint]trafficUpdate,
	owners map[uint]uint, bucket time.Time) error {
	byOwner := map[uint]uint64{}
	for id, d := range usage {
		if owner := owners[id]; owner != 0 {
			byOwner[owner] += d.Bytes
		}
	}

	// The customers: all three move together, so an allowance spent but
	// not split by direction never leaves the two pages disagreeing.
	// Incremented in SQL, so a reset from the UI meanwhile is not undone.
	for _, ids := range chunks(sortedIDs(usage)) {
		if err := addToRows(tx, "clients", []string{"used_bytes", "up_bytes", "down_bytes"}, ids,
			func(id uint) []uint64 { d := usage[id]; return []uint64{d.Bytes, d.Up, d.Down} }); err != nil {
			return err
		}
	}

	// The time series, bucketed on write: this bucket's sample of each
	// customer added to, or made. RX is what they received and TX what
	// they sent, as their own app has it.
	for _, ids := range chunks(sortedIDs(usage)) {
		// Asked by customer and bucket alone, which is what the index on
		// the two is for, the granularity checked here: asked for it too,
		// SQLite takes the granularity's index instead -- one value for
		// every fine sample there is -- and reads them all, each chunk.
		var found []struct {
			ID          uint
			ClientID    uint
			Granularity model.Granularity
		}
		if err := tx.Model(&model.TrafficSample{}).Select("id, client_id, granularity").
			Where("client_id IN ? AND bucket_ts = ?", ids, bucket).
			Scan(&found).Error; err != nil {
			return err
		}
		sampleOf := make(map[uint]uint, len(found))
		for _, f := range found {
			if f.Granularity != model.GranularityFine {
				continue
			}
			// The first made, as a look for one found it.
			if have, ok := sampleOf[f.ClientID]; !ok || f.ID < have {
				sampleOf[f.ClientID] = f.ID
			}
		}
		var existing []uint
		var fresh []model.TrafficSample
		amount := map[uint]usageDelta{} // sample id -> what it gains
		for _, id := range ids {
			d := usage[id]
			if sid, ok := sampleOf[id]; ok {
				existing = append(existing, sid)
				amount[sid] = d
				continue
			}
			fresh = append(fresh, model.TrafficSample{ClientID: id, BucketTS: bucket,
				Granularity: model.GranularityFine, RX: d.Down, TX: d.Up})
		}
		sort.Slice(existing, func(i, j int) bool { return existing[i] < existing[j] })
		if err := addToRows(tx, "traffic_samples", []string{"rx", "tx"}, existing,
			func(sid uint) []uint64 { d := amount[sid]; return []uint64{d.Down, d.Up} }); err != nil {
			return err
		}
		if len(fresh) > 0 {
			if err := tx.CreateInBatches(&fresh, writeChunk).Error; err != nil {
				return err
			}
		}
	}

	// What each file carried.
	for _, ids := range chunks(sortedIDs(devices)) {
		if err := addToRows(tx, "accounts", []string{"up_bytes", "down_bytes"}, ids,
			func(id uint) []uint64 { d := devices[id]; return []uint64{d.Up, d.Down} }); err != nil {
			return err
		}
	}

	if err := writeLiveness(tx, liveness); err != nil {
		return err
	}

	// The resellers' running totals, with the same deltas in the same
	// transaction; a handful of rows.
	for _, ownerID := range sortedIDs(byOwner) {
		delta := byOwner[ownerID]
		if delta == 0 {
			continue
		}
		if err := tx.Model(&model.Admin{}).Where("id = ?", ownerID).
			UpdateColumn("used_bytes", gorm.Expr("used_bytes + ?", delta)).Error; err != nil {
			return err
		}
	}
	return nil
}

// writeLiveness records when each file was last seen, and its handshake and
// endpoint where the reading had them, keeping what was there otherwise;
// updated_at moves, as an update through the model moved it.
func writeLiveness(tx *gorm.DB, liveness map[uint]trafficUpdate) error {
	when := "?"
	if tx.Dialector.Name() == "postgres" {
		when = "CAST(? AS TIMESTAMPTZ)"
	}
	now := time.Now()
	for _, ids := range chunks(sortedIDs(liveness)) {
		var vals strings.Builder
		// updated_at's comes first: it is bound where it stands in the text,
		// before the values.
		args := make([]any, 0, 1+len(ids)*4)
		args = append(args, now)
		for i, id := range ids {
			u := liveness[id]
			if i > 0 {
				vals.WriteString(", ")
			}
			fmt.Fprintf(&vals, "(CAST(? AS BIGINT), %s, %s, CAST(? AS TEXT))", when, when)
			var hs any
			if !u.Handshake.IsZero() {
				hs = u.Handshake
			}
			args = append(args, int64(id), u.At, hs, u.Endpoint)
		}
		q := "UPDATE accounts SET last_seen_at = v.column2, " +
			"last_handshake = COALESCE(v.column3, accounts.last_handshake), " +
			"last_endpoint = CASE WHEN v.column4 = '' THEN accounts.last_endpoint ELSE v.column4 END, " +
			"updated_at = " + when + " " +
			"FROM (VALUES " + vals.String() + ") AS v WHERE accounts.id = v.column1"
		if err := tx.Exec(q, args...).Error; err != nil {
			return err
		}
	}
	return nil
}
