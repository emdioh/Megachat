package messages

import (
	"sort"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/store"
)

// Ports of the dedup layer of server/messages.py (cross-key read union and
// intra-key twin merge) and of models.pick_survivor_and_merge.

type msg = store.Message

func strOf(p *string) string { return store.Deref(p) }

func statusRank(status *string) int {
	if status == nil {
		return 0
	}
	switch strings.ToLower(*status) {
	case "sent":
		return 1
	case "delivered":
		return 2
	case "read":
		return 3
	}
	return 0
}

// preferDisplaySender is models._prefer_display_sender.
func preferDisplaySender(a, b *string) *string {
	isDisplay := func(v *string) bool { return v != nil && *v != "" && !strings.Contains(*v, "@") }
	if isDisplay(a) {
		return a
	}
	if isDisplay(b) {
		return b
	}
	if a != nil && *a != "" {
		return a
	}
	return b
}

func missingStr(p *string) bool { return p == nil || *p == "" }

// survivorLess is models._survivor_sort_key ascending: status rank desc,
// real timestamp first, timestamp desc, rowid asc.
func survivorLess(a, b *msg) bool {
	ra, rb := statusRank(a.Status), statusRank(b.Status)
	if ra != rb {
		return ra > rb
	}
	if (a.Timestamp != 0) != (b.Timestamp != 0) {
		return a.Timestamp != 0
	}
	if a.Timestamp != b.Timestamp {
		return a.Timestamp > b.Timestamp
	}
	return a.RowID < b.RowID
}

// pickSurvivorAndMerge is models.pick_survivor_and_merge: it returns the
// survivor row with the merge overrides already applied (the Python
// “dict(row); update(overrides)“).
func pickSurvivorAndMerge(rows []msg, preferAttachment bool) msg {
	pool := make([]int, 0, len(rows))
	if preferAttachment {
		for i := range rows {
			if missingStr(rows[i].AttachmentID) {
				continue
			}
			pool = append(pool, i)
		}
	}
	if len(pool) == 0 {
		for i := range rows {
			pool = append(pool, i)
		}
	}
	best := pool[0]
	for _, i := range pool[1:] {
		if survivorLess(&rows[i], &rows[best]) {
			best = i
		}
	}
	survivor := rows[best]
	var others []*msg
	for i := range rows {
		if i != best {
			others = append(others, &rows[i])
		}
	}

	fillStr := func(get func(*msg) *string, set func(*msg, *string)) {
		if !missingStr(get(&survivor)) {
			return
		}
		for _, o := range others {
			if c := get(o); !missingStr(c) {
				set(&survivor, c)
				return
			}
		}
	}
	fillStr(func(m *msg) *string { return m.ContentType }, func(m *msg, v *string) { m.ContentType = v })
	fillStr(func(m *msg) *string { return m.MediaKind }, func(m *msg, v *string) { m.MediaKind = v })
	fillStr(func(m *msg) *string { return m.QuoteText }, func(m *msg, v *string) { m.QuoteText = v })
	if survivor.QuoteTimestamp == nil {
		for _, o := range others {
			if o.QuoteTimestamp != nil {
				survivor.QuoteTimestamp = o.QuoteTimestamp
				break
			}
		}
	}
	fillStr(func(m *msg) *string { return m.QuoteAuthor }, func(m *msg, v *string) { m.QuoteAuthor = v })
	fillStr(func(m *msg) *string { return m.ReplyToMessageID }, func(m *msg, v *string) { m.ReplyToMessageID = v })
	fillStr(func(m *msg) *string { return m.QuoteAttachmentID }, func(m *msg, v *string) { m.QuoteAttachmentID = v })
	fillStr(func(m *msg) *string { return m.QuoteAttachmentPath }, func(m *msg, v *string) { m.QuoteAttachmentPath = v })
	fillStr(func(m *msg) *string { return m.QuoteContentType }, func(m *msg, v *string) { m.QuoteContentType = v })
	fillStr(func(m *msg) *string { return m.AttachmentInfo }, func(m *msg, v *string) { m.AttachmentInfo = v })

	if survivor.Timestamp == 0 {
		for _, o := range others {
			if o.Timestamp != 0 {
				survivor.Timestamp = o.Timestamp
				break
			}
		}
	}
	fillStr(func(m *msg) *string { return m.Status }, func(m *msg, v *string) { m.Status = v })

	sender := survivor.Sender
	for _, o := range others {
		sender = preferDisplaySender(sender, o.Sender)
	}
	survivor.Sender = sender

	edited := false
	for i := range rows {
		if rows[i].Edited {
			edited = true
		}
	}
	survivor.Edited = edited
	return survivor
}

// realCaption is _real_caption: the stripped text when it is a real caption.
func realCaption(text *string, attachmentID *string, mediaFilename string) (string, bool) {
	stripped := pyStrip(strOf(text))
	if stripped == "" {
		return "", false
	}
	if isWhatsAppSyntheticMediaText(stripped, attachmentID) {
		return "", false
	}
	if mediaFilename != "" && isDuplicateMediaIdentity(stripped, mediaFilename) {
		return "", false
	}
	return stripped, true
}

// dedupIntraKeyRows is _dedup_intra_key_rows.
func dedupIntraKeyRows(rows []msg) []msg {
	type group struct {
		hasID       bool
		msgID       string
		isMine      bool
		rows        []msg
		attachments map[string]bool
	}
	var groups []*group
	for _, row := range rows {
		if missingStr(row.MsgID) {
			groups = append(groups, &group{rows: []msg{row}, attachments: map[string]bool{}})
			continue
		}
		attID := strOf(row.AttachmentID)
		var target *group
		for _, g := range groups {
			if !g.hasID || g.msgID != *row.MsgID || g.isMine != row.IsMine {
				continue
			}
			if attID == "" || len(g.attachments) == 0 || g.attachments[attID] {
				target = g
				break
			}
		}
		if target == nil {
			target = &group{hasID: true, msgID: *row.MsgID, isMine: row.IsMine, attachments: map[string]bool{}}
			groups = append(groups, target)
		}
		target.rows = append(target.rows, row)
		if attID != "" {
			target.attachments[attID] = true
		}
	}

	var merged []msg
	for _, g := range groups {
		if len(g.rows) < 2 {
			merged = append(merged, g.rows...)
			continue
		}
		hasMedia := false
		for i := range g.rows {
			if !missingStr(g.rows[i].AttachmentID) {
				hasMedia = true
			}
		}
		survivor := pickSurvivorAndMerge(g.rows, hasMedia)
		mediaFilename := ""
		if !missingStr(survivor.AttachmentID) {
			if !missingStr(survivor.AttachmentInfo) {
				mediaFilename = *survivor.AttachmentInfo
			} else {
				mediaFilename = attachmentBasename(*survivor.AttachmentID)
			}
		}
		var captions []string
		for i := range g.rows {
			if c, ok := realCaption(g.rows[i].Text, survivor.AttachmentID, mediaFilename); ok {
				if !containsStr(captions, c) {
					captions = append(captions, c)
				}
			}
		}
		if len(captions) > 1 {
			merged = append(merged, g.rows...)
			continue
		}
		if len(captions) == 1 {
			if _, ok := realCaption(survivor.Text, survivor.AttachmentID, mediaFilename); !ok {
				c := captions[0]
				survivor.Text = &c
			}
		}
		merged = append(merged, survivor)
	}
	sort.SliceStable(merged, func(i, j int) bool {
		if merged[i].Timestamp != merged[j].Timestamp {
			return merged[i].Timestamp < merged[j].Timestamp
		}
		return merged[i].RowID < merged[j].RowID
	})
	return merged
}

// crossKeyDedupRows is _cross_key_dedup_rows.
func crossKeyDedupRows(rows []msg, canonical string) []msg {
	idx := dedupCrossKey(len(rows), canonical,
		func(i int) string { return rows[i].ContactNumber },
		func(i int) identity {
			r := &rows[i]
			return identity{
				id: strOf(r.MsgID), isMine: r.IsMine, text: strOf(r.Text), timestamp: r.Timestamp,
				msgType: strOf(r.MsgType), attachmentID: strOf(r.AttachmentID),
			}
		})
	kept := make([]msg, len(idx))
	for i, j := range idx {
		kept[i] = rows[j]
	}
	sort.SliceStable(kept, func(i, j int) bool {
		if kept[i].Timestamp != kept[j].Timestamp {
			return kept[i].Timestamp < kept[j].Timestamp
		}
		return kept[i].RowID < kept[j].RowID
	})
	return kept
}
