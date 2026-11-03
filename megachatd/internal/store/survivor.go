package store

import "sort"

// mergeFillFields is models.MERGE_FILL_FIELDS: completed on the survivor
// only when missing (None/""), never overwritten.
var mergeFillFields = []string{
	"content_type",
	"media_kind",
	"quote_text",
	"quote_timestamp",
	"quote_author",
	"reply_to_message_id",
	"quote_attachment_id",
	"quote_attachment_path",
	"quote_content_type",
	"attachment_info",
}

// override is one column assignment computed by pickSurvivorAndMerge.
type override struct {
	col string
	val any
}

type overrides []override

func (o *overrides) set(col string, val any) {
	for i := range *o {
		if (*o)[i].col == col {
			(*o)[i].val = val
			return
		}
	}
	*o = append(*o, override{col, val})
}

func (o overrides) get(col string) any {
	for _, e := range o {
		if e.col == col {
			return e.val
		}
	}
	return nil
}

// pickSurvivorAndMerge is models.pick_survivor_and_merge (without the
// prefer_attachment option, which db.py never uses).  It returns the index of
// the survivor and the column overrides to apply to it.
func pickSurvivorAndMerge(rows []rowMap) (int, overrides) {
	if len(rows) == 0 {
		panic("pickSurvivorAndMerge: no rows")
	}
	idx := make([]int, len(rows))
	for i := range idx {
		idx[i] = i
	}
	// min() with a stable tie-break on input order.
	sort.SliceStable(idx, func(a, b int) bool {
		return survivorLess(rows[idx[a]], rows[idx[b]])
	})
	si := idx[0]
	survivor := rows[si]
	var others []rowMap
	for i, r := range rows {
		if i != si {
			others = append(others, r)
		}
	}

	var ov overrides
	for _, name := range mergeFillFields {
		value := survivor[name]
		if isMissing(value) {
			for _, o := range others {
				if c := o[name]; !isMissing(c) {
					value = c
					break
				}
			}
		}
		ov.set(name, value)
	}

	ts := asInt(survivor["timestamp"])
	if ts == 0 {
		for _, o := range others {
			if c := asInt(o["timestamp"]); c != 0 {
				ts = c
				break
			}
		}
		if ts != 0 {
			ov.set("timestamp", ts)
		}
	}

	status := survivor["status"]
	if isMissing(status) {
		for _, o := range others {
			if c := o["status"]; !isMissing(c) {
				status = c
				break
			}
		}
		if !isMissing(status) {
			ov.set("status", status)
		}
	}

	sender := survivor.str("sender")
	for _, o := range others {
		sender = preferDisplaySender(sender, o.str("sender"))
	}
	if sender == nil {
		ov.set("sender", nil)
	} else {
		ov.set("sender", *sender)
	}

	var edited int64
	for i, r := range rows {
		if v := asInt(r["edited"]); i == 0 || v > edited {
			edited = v
		}
	}
	ov.set("edited", edited)
	return si, ov
}

// survivorLess orders by status rank DESC, real timestamp first, timestamp
// DESC, id ASC (models._survivor_sort_key).
func survivorLess(a, b rowMap) bool {
	ra, rb := statusRank(a.str("status")), statusRank(b.str("status"))
	if ra != rb {
		return ra > rb
	}
	ta, tb := asInt(a["timestamp"]), asInt(b["timestamp"])
	if (ta != 0) != (tb != 0) {
		return ta != 0
	}
	if ta != tb {
		return ta > tb
	}
	return asInt(a["id"]) < asInt(b["id"])
}
