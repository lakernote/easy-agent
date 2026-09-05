package weixin

import (
	"encoding/binary"
	"testing"
)

func TestDecodeVoiceKeepsStandardAudio(t *testing.T) {
	wav := append([]byte("RIFF"), make([]byte, 48)...)
	decoded, mimeType, name, err := DecodeVoice(wav, 24000)
	if err != nil || string(decoded) != string(wav) || mimeType != "audio/wav" || name != "weixin-voice.wav" {
		t.Fatalf("unexpected WAV result: mime=%s name=%s err=%v", mimeType, name, err)
	}
	decoded, mimeType, name, err = DecodeVoice([]byte("OggSfixture"), 24000)
	if err != nil || mimeType != "audio/ogg" || name != "weixin-voice.ogg" || string(decoded) != "OggSfixture" {
		t.Fatalf("unexpected Ogg result: mime=%s name=%s err=%v", mimeType, name, err)
	}
}

func TestPCMToWAVWritesValidHeader(t *testing.T) {
	pcm := []byte{1, 2, 3, 4}
	wav := pcmToWAV(pcm, 24000)
	if string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || string(wav[36:40]) != "data" {
		t.Fatalf("invalid WAV header: %q", wav[:44])
	}
	if got := binary.LittleEndian.Uint32(wav[24:28]); got != 24000 {
		t.Fatalf("sample rate = %d", got)
	}
	if got := binary.LittleEndian.Uint32(wav[40:44]); got != uint32(len(pcm)) {
		t.Fatalf("data length = %d", got)
	}
}

func TestDecodeVoiceRejectsMalformedSilkWithoutPanic(t *testing.T) {
	if _, _, _, err := DecodeVoice([]byte("not-silk"), 24000); err == nil {
		t.Fatal("malformed SILK should fail")
	}
}
