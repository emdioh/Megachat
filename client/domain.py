"""Contact helpers for the frontends (no UI imports, no server code)."""

from __future__ import annotations

from models import PROTOCOL_WHATSAPP, ChatContact

__all__ = ["looks_unresolved", "search_contacts"]


def _jid_digits(jid: str) -> str:
    """Return only the digits of a JID (the phone for ``@c.us``), or ``""``."""
    return "".join(ch for ch in jid if ch.isdigit())


def _looks_like_phone(name: str, phone: str | None) -> bool:
    """True when *name* is the number fallback of *phone* (digits compared)."""
    if not name or not phone:
        return False
    name_digits = _jid_digits(name)
    phone_digits = _jid_digits(phone)
    if not name_digits or not phone_digits:
        return False
    return name_digits == phone_digits


def _is_placeholder_display_name(
    display_name: str, contact_id: str, phone: str | None
) -> bool:
    """True when a WhatsApp display name is a replaceable fallback, never a
    real name: empty, the id itself, the bare ``@lid`` digits, or the phone."""
    if not display_name:
        return True
    if display_name == contact_id:
        return True
    if contact_id.endswith("@lid") and display_name == contact_id.split("@", 1)[0]:
        return True
    return _looks_like_phone(display_name, phone or _jid_digits(contact_id))


def looks_unresolved(contact: ChatContact) -> bool:
    """True when *contact* still shows a placeholder instead of a real name."""
    if contact.protocol == PROTOCOL_WHATSAPP:
        return _is_placeholder_display_name(
            contact.display_name, contact.id, contact.extras.get("phone")
        )
    return (
        not contact.display_name
        or contact.display_name == contact.id
        or contact.display_name.isdigit()
    )


def search_contacts(
    contacts: list[ChatContact], query: str, max_results: int = 50
) -> list[ChatContact]:
    """Filter *contacts* by *query* (case-insensitive) on name, id or phone.

    The query is stripped; an empty query returns all contacts (up to
    *max_results*).  Matches keep their original order.
    """
    q = query.strip().lower()
    if not q:
        return contacts[:max_results]

    results: list[ChatContact] = []
    for contact in contacts:
        name = contact.display_name.lower()
        cid = contact.id.lower()
        phone = contact.phone.lower()
        if q in name or q in cid or q in phone:
            results.append(contact)
            if len(results) >= max_results:
                break
    return results
