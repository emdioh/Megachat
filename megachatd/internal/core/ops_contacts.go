package core

import (
	"context"
	"errors"
	"strings"

	"github.com/emdioh/megachat/megachatd/internal/api"
	"github.com/emdioh/megachat/megachatd/internal/ops"
	"github.com/emdioh/megachat/megachatd/internal/proto"
	"github.com/emdioh/megachat/megachatd/internal/store"
)

// searchMax is search_contacts' default max_results (not the picker limit).
const searchMax = 50

func (c *Core) contactOut(ct proto.Contact, unread map[store.ContactKey]int) api.ContactOut {
	extras := make(map[string]any, len(ct.Extras))
	for k, v := range ct.Extras {
		extras[k] = v
	}
	n := 0
	for _, key := range ContactStorageKeys(ct) {
		n += unread[store.ContactKey{Protocol: ct.Protocol, Contact: key}]
	}
	return api.ContactOut{
		ID: ct.ID, DisplayName: ct.DisplayName, Protocol: ct.Protocol, Extras: extras,
		LastMessageTs: ct.LastMessageTS(), Unread: n,
	}
}

func (c *Core) unreadCounts(ctx context.Context) map[store.ContactKey]int {
	m, err := c.st.UnreadCounts(ctx)
	if err != nil {
		return map[store.ContactKey]int{}
	}
	return m
}

func (c *Core) opContactsList(ctx context.Context, req api.ContactsListRequest) ([]api.ContactOut, error) {
	// Realign the WhatsApp unread badge with the phone first (best effort).
	if b, ok := c.mgr.Get(api.ProtocolWhatsApp); ok {
		if r, ok := b.(UnreadReconciler); ok {
			r.ReconcileUnread(ctx)
		}
	}
	unread := c.unreadCounts(ctx)
	contacts := c.AllContacts(ctx)
	if req.Q != nil {
		if q := strings.TrimSpace(*req.Q); q != "" {
			contacts = SearchContacts(contacts, q, searchMax)
		}
	}
	out := make([]api.ContactOut, 0, len(contacts))
	for _, ct := range contacts {
		out = append(out, c.contactOut(ct, unread))
	}
	return out, nil
}

func (c *Core) opContactsBook(ctx context.Context, req api.ContactsBookRequest) ([]api.ContactOut, error) {
	unread := c.unreadCounts(ctx)
	q := strings.TrimSpace(req.Q)
	if q == "" {
		return []api.ContactOut{}, nil
	}
	book, _ := c.mgr.ListAddressBook(ctx, nil, req.Force)
	found := SearchContacts(book, q, searchMax)
	out := make([]api.ContactOut, 0, len(found))
	for _, ct := range found {
		out = append(out, c.contactOut(ct, unread))
	}
	return out, nil
}

func (c *Core) opContactsForProtocol(ctx context.Context, req api.ContactsForProtocolRequest) ([]api.ContactWire, error) {
	protocol := strings.TrimSpace(req.Protocol)
	list, ok := c.Contacts(ctx, protocol)
	if !ok {
		return nil, &ops.Error{Code: api.CodeBackendDown, Message: "Backend unavailable"}
	}
	out := make([]api.ContactWire, 0, len(list))
	for _, ct := range list {
		out = append(out, ct.ToWire())
	}
	return out, nil
}

func (c *Core) opContactsRegister(ctx context.Context, req api.ContactsRegisterRequest) (api.ContactWire, error) {
	id, protocol := strings.TrimSpace(req.ID), strings.TrimSpace(req.Protocol)
	if !api.IsProtocol(protocol) || id == "" {
		return api.ContactWire{}, invalidRequest()
	}
	if _, ok := c.mgr.Get(protocol); !ok {
		return api.ContactWire{}, &ops.Error{Code: api.CodeBackendDown, Message: "Backend unavailable"}
	}
	extras := make(map[string]any, len(req.Extras)+1)
	for k, v := range req.Extras {
		extras[k] = v
	}
	extras["ghost"] = true
	ct := proto.Contact{ID: id, DisplayName: req.DisplayName, Protocol: protocol, Extras: extras}
	c.RegisterContact(ct)
	return ct.ToWire(), nil
}

func (c *Core) opContactsAddressBook(ctx context.Context, req api.ContactsAddressBookRequest) (api.ContactsAddressBookOut, error) {
	if b, ok := c.mgr.Get(api.ProtocolWhatsApp); ok {
		if r, ok := b.(LIDResolverStarter); ok {
			r.StartLIDResolver()
		}
	}
	out := api.ContactsAddressBookOut{Contacts: []api.ContactWire{}, Errors: map[string]string{}}
	// An explicit empty scope selects nothing (Python: set() is not None).
	if req.Protocols != nil && len(req.Protocols) == 0 {
		return out, nil
	}
	contacts, failed := c.mgr.ListAddressBook(ctx, req.Protocols, req.Force)
	for _, ct := range contacts {
		out.Contacts = append(out.Contacts, ct.ToWire())
	}
	for p, err := range failed {
		msg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "" // str(concurrent.futures.TimeoutError()) is empty
		}
		out.Errors[p] = msg
	}
	return out, nil
}

func (c *Core) opContactsCheckNumber(ctx context.Context, req api.ContactsCheckNumberRequest) (api.ContactsCheckNumberOut, error) {
	phone := strings.TrimSpace(req.Phone)
	b, ok := c.mgr.Get(api.ProtocolWhatsApp)
	if !ok || phone == "" {
		return api.ContactsCheckNumberOut{}, nil
	}
	nc, ok := b.(NumberChecker)
	if !ok {
		return api.ContactsCheckNumberOut{}, nil
	}
	exists, err := nc.CheckNumberExists(ctx, phone)
	if err != nil {
		c.log.Debug("WhatsApp number-exists check failed", "err", err)
		return api.ContactsCheckNumberOut{}, nil
	}
	return api.ContactsCheckNumberOut{Exists: &exists}, nil
}
