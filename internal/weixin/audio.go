package weixin

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	silk "github.com/wdvxdr1123/go-silk"
)

// DecodeVoice converts WeChat's common Tencent SILK payload into a standard
// WAV file. Already-standard WAV, MP3 and Ogg payloads are kept as-is.
func DecodeVoice(data []byte, sampleRate int) ([]byte, string, string, error) {
	if len(data) == 0 {
		return nil, "", "", errors.New("微信语音为空")
	}
	switch {
	case bytes.HasPrefix(data, []byte("RIFF")):
		return data, "audio/wav", "weixin-voice.wav", nil
	case bytes.HasPrefix(data, []byte("OggS")):
		return data, "audio/ogg", "weixin-voice.ogg", nil
	case bytes.HasPrefix(data, []byte("ID3")) || (len(data) > 1 && data[0] == 0xff && data[1]&0xe0 == 0xe0):
		return data, "audio/mpeg", "weixin-voice.mp3", nil
	}
	if sampleRate != 8000 && sampleRate != 12000 && sampleRate != 16000 && sampleRate != 24000 && sampleRate != 48000 {
		sampleRate = 24000
	}
	pcm, err := decodeSILK(data, sampleRate)
	if err != nil {
		return nil, "", "", fmt.Errorf("解码微信 SILK 语音: %w", err)
	}
	if len(pcm) == 0 {
		return nil, "", "", errors.New("解码微信 SILK 语音后没有音频数据")
	}
	return pcmToWAV(pcm, sampleRate), "audio/wav", "weixin-voice.wav", nil
}

func decodeSILK(data []byte, sampleRate int) (pcm []byte, err error) {
	// The decoder is a generated Go port of the SILK C SDK. Treat malformed
	// remote media as an ordinary decode error instead of allowing a bounds
	// panic inside the codec to terminate the WeChat poller.
	defer func() {
		if recovered := recover(); recovered != nil {
			pcm = nil
			err = fmt.Errorf("无效的 SILK 数据: %v", recovered)
		}
	}()
	return silk.DecodeSilkBuffToPcm(data, sampleRate)
}

func pcmToWAV(pcm []byte, sampleRate int) []byte {
	result := bytes.NewBuffer(make([]byte, 0, 44+len(pcm)))
	result.WriteString("RIFF")
	_ = binary.Write(result, binary.LittleEndian, uint32(36+len(pcm)))
	result.WriteString("WAVEfmt ")
	_ = binary.Write(result, binary.LittleEndian, uint32(16))
	_ = binary.Write(result, binary.LittleEndian, uint16(1))
	_ = binary.Write(result, binary.LittleEndian, uint16(1))
	_ = binary.Write(result, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(result, binary.LittleEndian, uint32(sampleRate*2))
	_ = binary.Write(result, binary.LittleEndian, uint16(2))
	_ = binary.Write(result, binary.LittleEndian, uint16(16))
	result.WriteString("data")
	_ = binary.Write(result, binary.LittleEndian, uint32(len(pcm)))
	result.Write(pcm)
	return result.Bytes()
}
