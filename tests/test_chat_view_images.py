"""
Unit tests for the native (KITTY) branch of chat attachment resolution.

Covers ``_resolve_attachment_worker`` (KITTY), ``_finish_native_thumbnail`` and
``_resolve_mounted_image_paths`` in ``tui/chat_view.py`` — the CATIMG branch is
already covered by ``test_image_async_download.py``.  Headless: the renderer is
injected with a recording write callback and the UI-thread hops run inline.
"""

from __future__ import annotations

import io
import sys
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import MagicMock

from PIL import Image

PROJECT_ROOT = Path(__file__).resolve().parent.parent
sys.path.insert(0, str(PROJECT_ROOT))


from models import PROTOCOL_SIGNAL
from tui.app import SignalTUI
from tui.chat_view import (
    _effective_media_kind,
    _is_scrolled_to_bottom,
    _media_display_text,
)
from tui.images.detect import ImageSupport
from tui.images.kitty_renderer import KittyRenderer
from ui_components import ImageWidget, MessageWidget


def _make_png_bytes(width: int = 16, height: int = 32) -> bytes:
    buf = io.BytesIO()
    Image.new("RGB", (width, height), "red").save(buf, "PNG")
    return buf.getvalue()


def _make_kitty_app() -> tuple[SignalTUI, list[str]]:
    written: list[str] = []
    app = SignalTUI(image_support=ImageSupport.KITTY)
    app._native_renderer = KittyRenderer(write=written.append, cell_w=8, cell_h=16)
    app.core.manager = MagicMock()
    app.run_worker = MagicMock()
    app.call_from_thread = MagicMock(side_effect=lambda fn, *a, **k: fn(*a, **k))
    app._chat_log = _FakeChatLog()
    return app, written


class _FakeChatLog:
    """Minimal stand-in exposing what the chat image paths need."""

    def __init__(self, width: int = 60) -> None:
        self.content_region = SimpleNamespace(width=width)
        self.children: list = []
        self.max_scroll_y = 0
        self.scroll_offset = SimpleNamespace(y=0)
        self.scroll_end_calls = 0

    def mount(self, *widgets, before=None, after=None):
        for w in widgets:
            w._is_mounted = True
            self.children.append(w)

    def scroll_end(self, animate: bool = False) -> None:
        self.scroll_end_calls += 1


class TestNativePlaceholder:
    """Native mode shows no placeholder; degraded/fallback modes keep it."""

    def test_text_when_catimg(self):
        app = SignalTUI()  # default CATIMG, no renderer
        assert (
            app._native_placeholder("[🖼️ Image]", attachment_id="att-1") == "[🖼️ Image]"
        )


class TestMediaKindRendering:
    def test_effective_kind_supports_legacy_rows(self):
        assert _effective_media_kind({"media_kind": "voice"}) == "voice"
        assert (
            _effective_media_kind({"msg_type": "image", "content_type": "image/gif"})
            == "gif"
        )
        assert _effective_media_kind({"msg_type": "sticker"}) == "sticker"
        assert _effective_media_kind({"msg_type": "attachment"}) is None

    def test_media_display_text_uses_kind_icons_and_legacy_fallback(self):
        assert _media_display_text("", None, "attachment", "voice") == "🎤 [Voice]"
        assert (
            _media_display_text("", "clip.mp4", "attachment", "video") == "🎬 clip.mp4"
        )
        assert _media_display_text("", None, "attachment") == "📎 [File]"

    def test_media_display_text_url_and_mime_fall_back_to_basename(self):
        """N4: un ``attachment_info`` URL/mime-only non è un nome file; si usa
        il basename dell'``attachment_id`` (parità col layer web)."""
        assert (
            _media_display_text(
                "",
                "http://localhost:3000/api/files/default/voice.ogg",
                "attachment",
                "voice",
                "http://host/a/clip.ogg",
            )
            == "🎤 clip.ogg"
        )
        assert (
            _media_display_text(
                "", "audio/ogg", "attachment", "audio", "http://host/a/song.ogg"
            )
            == "🎵 song.ogg"
        )
        assert (
            _media_display_text(
                "", "image/jpeg", "attachment", None, "http://host/a/doc.pdf"
            )
            == "📎 doc.pdf"
        )

    def test_cached_gif_uses_image_widget(self):
        app = SignalTUI()
        widgets = app._build_message_widgets(
            PROTOCOL_SIGNAL,
            False,
            {
                "text": "",
                "msg_type": "attachment",
                "media_kind": "gif",
                "attachment_id": "gif-1",
                "attachment_info": "animation.gif",
            },
        )

        assert isinstance(widgets[0], ImageWidget)

    def test_cached_downloadable_sticker_is_small_image_without_caption(self):
        app = SignalTUI()
        widgets = app._build_message_widgets(
            PROTOCOL_SIGNAL,
            False,
            {
                "text": "caption must not become a bubble",
                "msg_type": "sticker",
                "media_kind": "sticker",
                "attachment_id": "sticker-1",
                "attachment_info": "sticker.webp",
            },
        )

        assert len(widgets) == 1
        assert isinstance(widgets[0], ImageWidget)
        assert widgets[0].has_class("msg-sticker")
        assert widgets[0]._msg_type == "sticker"

    def test_cached_signal_sticker_without_attachment_stays_text(self):
        app = SignalTUI()
        widgets = app._build_message_widgets(
            PROTOCOL_SIGNAL,
            False,
            {
                "text": "",
                "msg_type": "sticker",
                "media_kind": "sticker",
                "attachment_info": None,
            },
        )

        assert isinstance(widgets[0], MessageWidget)
        assert widgets[0]._msg_text == "🎨 [Sticker]"

    def test_non_image_media_widget_has_openable_reference(self):
        app = SignalTUI()
        widgets = app._build_message_widgets(
            PROTOCOL_SIGNAL,
            False,
            {
                "text": "",
                "msg_type": "attachment",
                "media_kind": "document",
                "attachment_id": "doc-1",
                "attachment_info": "manual.pdf",
            },
        )

        assert isinstance(widgets[0], MessageWidget)
        assert widgets[0]._attachment_ref == ("doc-1", PROTOCOL_SIGNAL)


class _ScrollFakeChatLog:
    """Fake ``#chat-log`` exposing a controllable scroll state (headless)."""

    def __init__(self, *, max_y: int, offset_y: int) -> None:
        self.max_scroll_y = max_y
        self.scroll_offset = SimpleNamespace(y=offset_y)
        self.scroll_end_calls = 0

    def scroll_end(self, animate: bool = False) -> None:
        self.scroll_end_calls += 1


class TestScrollAnchor:
    """_finish_native_thumbnail keeps the chat stuck to the bottom on growth."""

    def _mounted_widget(self) -> ImageWidget:
        widget = ImageWidget(attachment_path=None, attachment_id="att-1")
        widget._is_mounted = True
        return widget

    def _call(self, app, widget, log, tmp_path):
        app._chat_log = log
        app._finish_native_thumbnail(widget, tmp_path / "x.png", _make_png_bytes())

    def test_is_scrolled_to_bottom_tolerance(self):
        # At exactly max_y - 1 the 1-cell tolerance still counts as "bottom".
        assert _is_scrolled_to_bottom(_ScrollFakeChatLog(max_y=50, offset_y=49)) is True
        assert (
            _is_scrolled_to_bottom(_ScrollFakeChatLog(max_y=50, offset_y=48)) is False
        )
        assert _is_scrolled_to_bottom(_ScrollFakeChatLog(max_y=-3, offset_y=0)) is True
