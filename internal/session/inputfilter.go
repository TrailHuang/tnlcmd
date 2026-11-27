package session

import (
	"encoding/hex"

	"github.com/TrailHuang/tnlcmd/internal/debuglog"
)

// inputEventKind 是输入过滤器向调用方输出的事件类型。
// 过滤器只负责 Telnet/ANSI 序列解析与边界控制，具体行为由各读取路径处理。
type inputEventKind uint8

const (
	evChar inputEventKind = iota // 可打印字节（b 为原始字节）
	evEnter
	evBackspace
	evTab
	evQuestion
	evCtrlA
	evCtrlE
	evCtrlC
	evCtrlD
	evUp
	evDown
	evLeft
	evRight
	evHome
	evEnd
	evDelete
)

// inputEvent 是过滤器输出的一个输入事件。
type inputEvent struct {
	kind inputEventKind
	b    byte
}

// telnetInput 是三条读取路径共用的输入过滤器。
// 状态跨 read 批次保持，正确处理变长 IAC、子协商、ANSI 光标序列与 UTF-8 全角问号。
type telnetInput struct {
	state       inputState
	seqLen      int
	pendingEdit byte
}

// reset 将过滤器恢复到正常态（每条命令/每次读取开始时调用）。
func (f *telnetInput) reset() {
	f.state = stNormal
	f.seqLen = 0
	f.pendingEdit = 0
}

// feed 逐字节处理数据并通过 emit 输出事件。emit 返回 false 时立即停止，
// 返回已消费的字节数，调用方据此保存未消费的后备输入。
func (f *telnetInput) feed(data []byte, emit func(inputEvent) bool) int {
	for i, b := range data {
		// 未闭合序列的长度边界来自工单 08：超限回退正常态，避免永久挂起
		if f.state != stNormal {
			f.seqLen++
			if f.seqLen > maxSeqLen {
				f.reset()
				continue
			}
		} else {
			f.seqLen = 0
		}

		var ev inputEvent
		hasEvent := false
		complete := false

		switch f.state {
		case stIAC:
			switch {
			case isIAC3ByteCmd(b):
				f.state = stIACOpt
			case b == 0xFA: // SB
				f.state = stSB
			default: // 2 字节命令
				f.state = stNormal
				complete = true
			}
		case stIACOpt:
			f.state = stNormal
			complete = true
		case stSB:
			if b == 0xFF {
				f.state = stSBIAC
			}
			complete = true
		case stSBIAC:
			if b == 0xF0 { // SE
				f.state = stNormal
			} else {
				f.state = stSB
			}
			complete = true
		case stESC:
			if b == '[' {
				f.state = stESCBracket
			} else {
				f.state = stNormal
				complete = true
			}
		case stESCBracket:
			f.state = stNormal
			complete = true
			switch b {
			case 'A':
				ev.kind = evUp
			case 'B':
				ev.kind = evDown
			case 'C':
				ev.kind = evRight
			case 'D':
				ev.kind = evLeft
			case 'H':
				ev.kind = evHome
			case 'F':
				ev.kind = evEnd
			case '1', '3', '4', '7', '8':
				f.pendingEdit = b
				f.state = stESCTilde
			default:
				// 其他 ESC [ X 序列忽略
			}
			hasEvent = f.state == stNormal && ev.kind != 0
		case stESCTilde:
			complete = true
			switch {
			case b == '~':
				f.state = stNormal
				switch f.pendingEdit {
				case '3':
					ev.kind = evDelete
				case '1', '7':
					ev.kind = evHome
				case '4', '8':
					ev.kind = evEnd
				}
				hasEvent = ev.kind != 0
			case (b >= 'A' && b <= 'D') || b == 'H' || b == 'F':
				f.state = stNormal
				switch b {
				case 'D':
					ev.kind = evLeft
				case 'C':
					ev.kind = evRight
				}
				hasEvent = ev.kind != 0
			}
		case stUTF8Q1:
			if b == 0xBC {
				f.state = stUTF8Q2
			} else {
				f.state = stNormal
				complete = true
			}
		case stUTF8Q2:
			f.state = stNormal
			complete = true
			if b == 0x9F { // 全角问号 '？'
				ev.kind = evQuestion
				hasEvent = true
			}
		default:
			switch {
			case b == 0xFF:
				f.state = stIAC
				complete = true
			case b == 0x1B:
				f.state = stESC
				complete = true
			case b == 0xEF:
				f.state = stUTF8Q1
				complete = true
			default:
				ev.b = b
				hasEvent = true
				complete = true
				switch b {
				case 0x03:
					ev.kind = evCtrlC
				case 0x04:
					ev.kind = evCtrlD
				case 0x01:
					ev.kind = evCtrlA
				case 0x05:
					ev.kind = evCtrlE
				case 0x7F, 0x08:
					ev.kind = evBackspace
				case 0x09:
					ev.kind = evTab
				case 0x3F:
					ev.kind = evQuestion
				case 0x0D, 0x0A:
					ev.kind = evEnter
				default:
					if b >= 0x20 && b <= 0x7E {
						ev.kind = evChar
					} else {
						hasEvent = false
					}
				}
			}
		}

		if hasEvent && !emit(ev) {
			return i + 1
		}
		_ = complete
	}
	return len(data)
}

// readEvents 从会话读取缓冲取数据并喂给过滤器。
// 事件处理函数返回 false 时，未消费的字节保存在 s.pendingInput 供下次读取使用。
func (s *Session) readEvents(f *telnetInput, emit func(inputEvent) bool) error {
	for {
		data := s.pendingInput
		if len(data) == 0 {
			n, err := s.reader.Read(s.readBuf)
			if err != nil {
				return err
			}
			data = s.readBuf[:n]
		}
		if debuglog.Enabled() {
			debuglog.Debug("session", "input_received",
				"remote", s.remoteStr(),
				"n", len(data),
				"state", f.state,
				"pending", len(s.pendingInput),
				"prefix_hex", hex.EncodeToString(data[:min(len(data), 8)]))
		}
		consumed := f.feed(data, emit)
		if consumed < len(data) {
			s.pendingInput = append(s.pendingInput[:0], data[consumed:]...)
		} else {
			s.pendingInput = s.pendingInput[:0]
		}
		return nil
	}
}
