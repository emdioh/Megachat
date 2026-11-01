from __future__ import annotations

from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from transcription.client import (
    _MAX_AUDIO_SIZE,
    CloudTranscriptionClient,
    TranscriptionError,
)


class RecordingFormData:
    def __init__(self) -> None:
        self.fields = []

    def add_field(self, name, value, **kwargs) -> None:
        self.fields.append((name, value, kwargs))


def _aiohttp_mocks(*, status=200, json_data=None, body=""):
    response = MagicMock(status=status)
    response.json = AsyncMock(return_value=json_data or {})
    response.text = AsyncMock(return_value=body)

    response_context = MagicMock()
    response_context.__aenter__ = AsyncMock(return_value=response)
    response_context.__aexit__ = AsyncMock(return_value=None)

    session = MagicMock()
    session.post.return_value = response_context
    session_context = MagicMock()
    session_context.__aenter__ = AsyncMock(return_value=session)
    session_context.__aexit__ = AsyncMock(return_value=None)
    return response, session, session_context


@pytest.mark.asyncio
async def test_rejects_audio_larger_than_25_mb(tmp_path: Path):
    audio = tmp_path / "large.mp3"
    with audio.open("wb") as stream:
        stream.truncate(_MAX_AUDIO_SIZE + 1)
    client = CloudTranscriptionClient("secret")

    with pytest.raises(TranscriptionError, match="max 25MB"):
        await client.transcribe_async(audio)


@pytest.mark.asyncio
async def test_upload_sends_multipart_auth_and_parses_text(tmp_path: Path):
    audio = tmp_path / "voice.mp3"
    audio.write_bytes(b"audio")
    form = RecordingFormData()
    _, session, session_context = _aiohttp_mocks(
        json_data={"text": "  testo trascritto  "}
    )
    client = CloudTranscriptionClient(
        "secret", base_url="https://example.test/v1/", model="gpt-transcribe"
    )

    with (
        patch("transcription.client.aiohttp.FormData", return_value=form),
        patch(
            "transcription.client.aiohttp.ClientSession",
            return_value=session_context,
        ) as session_factory,
    ):
        result = await client.transcribe_async(audio)

    assert result == "testo trascritto"
    fields = {name: (value, kwargs) for name, value, kwargs in form.fields}
    assert fields["model"][0] == "gpt-transcribe"
    assert fields["file"][1]["filename"] == "voice.mp3"
    assert Path(fields["file"][0].name) == audio
    session.post.assert_called_once()
    _, kwargs = session.post.call_args
    assert session.post.call_args.args[0] == (
        "https://example.test/v1/audio/transcriptions"
    )
    assert kwargs["data"] is form
    assert kwargs["headers"] == {"Authorization": "Bearer secret"}
    assert session_factory.call_args.kwargs["timeout"].total == 120


@pytest.mark.asyncio
async def test_http_error_raises_transcription_error(tmp_path: Path):
    audio = tmp_path / "voice.mp3"
    audio.write_bytes(b"audio")
    _, _, session_context = _aiohttp_mocks(status=429, body="rate limited")
    client = CloudTranscriptionClient("secret")

    with (
        patch(
            "transcription.client.aiohttp.ClientSession",
            return_value=session_context,
        ),
        pytest.raises(TranscriptionError, match="HTTP 429: rate limited"),
    ):
        await client.transcribe_async(audio)


@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("model", "expected_field"),
    [("gpt-transcribe", "languages"), ("whisper-1", "language")],
)
async def test_language_field_depends_on_model(
    tmp_path: Path, model: str, expected_field: str
):
    audio = tmp_path / "voice.wav"
    audio.write_bytes(b"audio")
    form = RecordingFormData()
    _, _, session_context = _aiohttp_mocks(json_data={"text": "ok"})
    client = CloudTranscriptionClient("secret", model=model, language="it")

    with (
        patch("transcription.client.aiohttp.FormData", return_value=form),
        patch(
            "transcription.client.aiohttp.ClientSession",
            return_value=session_context,
        ),
    ):
        await client.transcribe_async(audio)

    names = [name for name, _, _ in form.fields]
    assert expected_field in names
    assert ({"language", "languages"} - {expected_field}).isdisjoint(names)
    assert (
        next(value for name, value, _ in form.fields if name == expected_field) == "it"
    )


@pytest.mark.asyncio
async def test_unsupported_extension_is_transcoded_before_upload(tmp_path: Path):
    source = tmp_path / "voice.amr"
    source.write_bytes(b"opus")
    transcoded = tmp_path / "converted.mp3"
    transcoded.write_bytes(b"mp3")
    client = CloudTranscriptionClient("secret")
    client._upload = AsyncMock(return_value="testo")

    with patch(
        "transcription.client._transcode_to_mp3", return_value=transcoded
    ) as transcode:
        result = await client.transcribe_async(source)

    assert result == "testo"
    transcode.assert_called_once_with(source)
    client._upload.assert_awaited_once_with(transcoded)
    assert not transcoded.exists()


@pytest.mark.asyncio
async def test_unsupported_format_http_error_retries_after_transcoding(tmp_path: Path):
    source = tmp_path / "voice.wav"
    source.write_bytes(b"wav")
    transcoded = tmp_path / "converted.mp3"
    transcoded.write_bytes(b"mp3")
    client = CloudTranscriptionClient("secret")
    client._upload = AsyncMock(
        side_effect=[TranscriptionError("400 Unsupported file format"), "testo"]
    )

    with (
        patch("transcription.client._ffmpeg_available", return_value=True),
        patch(
            "transcription.client._transcode_to_mp3", return_value=transcoded
        ) as transcode,
    ):
        result = await client.transcribe_async(source)

    assert result == "testo"
    transcode.assert_called_once_with(source)
    assert client._upload.await_args_list[0].args == (source,)
    assert client._upload.await_args_list[1].args == (transcoded,)
    assert not transcoded.exists()


@pytest.mark.asyncio
@pytest.mark.parametrize("suffix", [".oga", ".opus"])
async def test_ogg_voice_note_is_sent_as_ogg_without_ffmpeg(tmp_path: Path, suffix):
    source = tmp_path / f"voice{suffix}"
    source.write_bytes(b"OggS")
    client = CloudTranscriptionClient("secret")
    client._upload = AsyncMock(return_value="testo")

    with patch("transcription.client._transcode_to_mp3") as transcode:
        result = await client.transcribe_async(source)

    assert result == "testo"
    transcode.assert_not_called()
    client._upload.assert_awaited_once_with(source, filename="voice.ogg")


@pytest.mark.asyncio
async def test_ogg_upload_sets_ogg_filename(tmp_path: Path):
    source = tmp_path / "voice.oga"
    source.write_bytes(b"OggS")
    _, session, session_context = _aiohttp_mocks(json_data={"text": "ok"})
    client = CloudTranscriptionClient("secret")
    with patch(
        "transcription.client.aiohttp.ClientSession", return_value=session_context
    ):
        assert await client.transcribe_async(source) == "ok"
    form = session.post.call_args.kwargs["data"]
    file_field = next(f for f in form._fields if f[0]["name"] == "file")
    assert file_field[0]["filename"] == "voice.ogg"


@pytest.mark.asyncio
async def test_ogg_rejected_without_ffmpeg_explains(tmp_path: Path):
    source = tmp_path / "voice.oga"
    source.write_bytes(b"OggS")
    client = CloudTranscriptionClient("secret")
    client._upload = AsyncMock(
        side_effect=TranscriptionError("Errore trascrizione HTTP 400: bad format")
    )
    with (
        patch("transcription.client._ffmpeg_available", return_value=False),
        pytest.raises(TranscriptionError, match="ffmpeg non è installato"),
    ):
        await client.transcribe_async(source)
