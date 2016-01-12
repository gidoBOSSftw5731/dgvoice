/*******************************************************************************
 * This is very experimental code and probably a long way from perfect or
 * ideal.  Please provide feed back on areas that would improve performance
 *
 */
package dgvoice

import (
	"encoding/binary"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/layeh/gopus"
)

// NOTE: This API is not final and these are likely to change.
// Settings, these can be modified but they will not effect any
// currently running process.
var (
	// 1 for mono, 2 for stereo
	Channels int = 2

	// sample rate of frames, need to test valid options
	FrameRate int = 48000

	// Length of audio frame in ms can be 20, 40, or 60
	FrameTime int = 60
)

// Internal global vars.
// NOTE: This API is not final and these are likely to change.
var (
	opusEncoder *gopus.Encoder
	sequence    uint16
	timestamp   uint32
	run         *exec.Cmd
	FrameLength = func() int { return ((FrameRate / 1000) * FrameTime) } // Length of frame as uint16 array
	OpusMaxSize = func() int { return (FrameLength() * Channels * 2) }   // max size opus encoder can return
)

// init setups up the package for use :)
func init() {

	sequence = 0
	timestamp = 0
}

// KillPlayer forces the player to stop by killing the ffmpeg cmd process
// this method may be removed later in favor of using chans or bools to
// request a stop.
func KillPlayer() {
	run.Process.Kill()
}

// PlayAudioFile will play the given filename to the already connected
// Discord voice server/channel.  voice websocket and udp socket
// must already be setup before this will work.
func PlayAudioFile(s *discordgo.Session, filename string) {

	opusMaxSize := OpusMaxSize()
	frameLength := FrameLength()
	frameRate := FrameRate
	frameTime := FrameTime
	channels := Channels

	// Create a shell command "object" to run.
	run = exec.Command("ffmpeg", "-i", filename, "-f", "s16le", "-ar", strconv.Itoa(frameRate), "-ac", strconv.Itoa(channels), "pipe:1")
	stdout, err := run.StdoutPipe()
	if err != nil {
		fmt.Println("StdoutPipe Error:", err)
		return
	}

	// Starts the ffmpeg command
	err = run.Start()
	if err != nil {
		fmt.Println("RunStart Error:", err)
		return
	}

	opusEncoder, err = gopus.NewEncoder(frameRate, channels, gopus.Audio)
	if err != nil {
		fmt.Println("NewEncoder Error:", err)
		return
	}

	// variables used during loop below
	udpPacket := make([]byte, opusMaxSize)
	audiobuf := make([]int16, frameLength*channels)

	// build the parts that don't change in the udpPacket.
	udpPacket[0] = 0x80
	udpPacket[1] = 0x78
	binary.BigEndian.PutUint32(udpPacket[8:], s.Voice.OP2.SSRC)

	// Send "speaking" packet over the voice websocket
	s.Voice.Speaking(true)
	// Send not "speaking" packet over the websocket when we finish
	defer s.Voice.Speaking(false)

	// start a read/encode/send loop that loops until EOF from ffmpeg
	ticker := time.NewTicker(time.Millisecond * time.Duration(frameTime))
	for {

		// Add sequence and timestamp to udpPacket
		binary.BigEndian.PutUint16(udpPacket[2:], sequence)
		binary.BigEndian.PutUint32(udpPacket[4:], timestamp)

		// read data from ffmpeg stdout
		err = binary.Read(stdout, binary.LittleEndian, &audiobuf)
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return
		}
		if err != nil {
			fmt.Println("Playback Error:", err)
			return
		}

		// try encoding ffmpeg frame with Opus
		opus, err := opusEncoder.Encode(audiobuf, frameLength, opusMaxSize)
		if err != nil {
			fmt.Println("Encoding Error:", err)
			return
		}

		// copy opus result into udpPacket
		copy(udpPacket[12:], opus)

		// block here until we're exactly at the right time :)
		<-ticker.C

		// Send rtp audio packet to Discord over UDP
		s.Voice.UDPConn.Write(udpPacket[:(len(opus) + 12)])

		if (sequence) == 0xFFFF {
			sequence = 0
		} else {
			sequence += 1
		}

		if (timestamp + uint32(frameLength)) >= 0xFFFFFFFF {
			timestamp = 0
		} else {
			timestamp += uint32(frameLength)
		}
	}
}
