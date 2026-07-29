package ffmpeg

import (
	"bytes"
	"reflect"
	"testing"
)

func TestDecodeMJPEG(t *testing.T) {
	input := []byte{
		0x00, 0xff, 0xd8, 0x01, 0x02, 0xff, 0xd9,
		0x7f, 0xff, 0xd8, 0x03, 0xff, 0xd9,
	}
	var frames [][]byte
	err := decodeMJPEG(bytes.NewReader(input), 1024, func(frame []byte) error {
		frames = append(frames, frame)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want := [][]byte{
		{0xff, 0xd8, 0x01, 0x02, 0xff, 0xd9},
		{0xff, 0xd8, 0x03, 0xff, 0xd9},
	}
	if !reflect.DeepEqual(frames, want) {
		t.Fatalf("frames = %v, want %v", frames, want)
	}
}

func TestDecodeMJPEGRejectsOversizedFrame(t *testing.T) {
	input := []byte{0xff, 0xd8, 0x01, 0x02, 0x03, 0xff, 0xd9}
	if err := decodeMJPEG(bytes.NewReader(input), 4, func([]byte) error { return nil }); err == nil {
		t.Fatal("expected oversized frame error")
	}
}
