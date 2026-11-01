package utils

import (
	"bytes"
	"encoding/binary"
)

var oggs = []byte("OggS")

type PCMConfig struct {
	SampleRate    int
	NumChannels   int
	BitsPerSample int
}

var aifc = []byte("AIFC")
var riff = []byte("RIFF")
var adif = []byte("ADIF")
var aiff = []byte("AIFF")
var wave = []byte("WAVE")
var form = []byte("FORM")
var id3 = []byte("ID3")
var flac = []byte("fLaC")

func DefaultGeminiPCMConfig() PCMConfig {
	return PCMConfig{
		SampleRate:    24000,
		NumChannels:   1,
		BitsPerSample: 16,
	}
}

func ConvertPCMToWAV(pcmData []byte, config PCMConfig) ([]byte, error) {
	byteRate := config.SampleRate * config.NumChannels * config.BitsPerSample / 8
	blockAlign := config.NumChannels * config.BitsPerSample / 8

	dataSize := uint32(len(pcmData))
	fileSize := 36 + dataSize

	var buf bytes.Buffer

	buf.WriteString("RIFF")
	binary.Write(&buf, binary.LittleEndian, fileSize)
	buf.WriteString("WAVE")

	buf.WriteString("fmt ")
	binary.Write(&buf, binary.LittleEndian, uint32(16))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(config.NumChannels))
	binary.Write(&buf, binary.LittleEndian, uint32(config.SampleRate))
	binary.Write(&buf, binary.LittleEndian, uint32(byteRate))
	binary.Write(&buf, binary.LittleEndian, uint16(blockAlign))
	binary.Write(&buf, binary.LittleEndian, uint16(config.BitsPerSample))

	buf.WriteString("data")
	binary.Write(&buf, binary.LittleEndian, dataSize)
	buf.Write(pcmData)

	return buf.Bytes(), nil
}

func DetectAudioMimeType(audioData []byte) string {
	if len(audioData) < 4 {
		return "audio/mp3"
	}

	if len(audioData) >= 12 &&
		bytes.Equal(audioData[:4], riff) &&
		bytes.Equal(audioData[8:12], wave) {
		return "audio/wav"
	}

	if len(audioData) >= 3 && bytes.Equal(audioData[:3], id3) {
		return "audio/mp3"
	}

	if bytes.HasPrefix(audioData, adif) {
		return "audio/aac"
	}
	if len(audioData) >= 2 && audioData[0] == 0xFF && (audioData[1]&0xF6) == 0xF0 {
		return "audio/aac"
	}

	if len(audioData) >= 12 && bytes.Equal(audioData[:4], form) &&
		(bytes.Equal(audioData[8:12], aiff) || bytes.Equal(audioData[8:12], aifc)) {
		return "audio/aiff"
	}

	if bytes.HasPrefix(audioData, flac) {
		return "audio/flac"
	}

	if bytes.HasPrefix(audioData, oggs) {
		return "audio/ogg"
	}

	if len(audioData) >= 2 && audioData[0] == 0xFF &&
		(audioData[1] == 0xFB || audioData[1] == 0xF3 || audioData[1] == 0xF2 || audioData[1] == 0xFA) {
		return "audio/mp3"
	}

	return "audio/mp3"
}

func AudioFilenameFromBytes(audioData []byte) string {
	mimeToExt := map[string]string{
		"audio/wav":  "audio.wav",
		"audio/aac":  "audio.aac",
		"audio/aiff": "audio.aiff",
		"audio/flac": "audio.flac",
		"audio/ogg":  "audio.ogg",
	}
	mime := DetectAudioMimeType(audioData)
	if ext, ok := mimeToExt[mime]; ok {
		return ext
	}
	return "audio.mp3"
}
