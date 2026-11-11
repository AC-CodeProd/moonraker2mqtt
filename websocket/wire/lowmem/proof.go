package websocket

import (
	"io"
	"sync/atomic"
)

// FrameProofDiagnostics is link-time opt-in; OFF still retains the ABI fields.
var FrameProofDiagnostics string
var proofIDs uint32
var proofIDsExhausted uint32

type proofGeometry struct {
	Seq, Start, Length, End uint64
	Header                  uint8
	Bits, Code              byte
	Mask                    bool
	Sum                     [8]uint32
}
type proofStream struct {
	id           uint32
	bad, emitted bool
	// Transport observer. Only called synchronously at Reader.Read return, before
	// bufio can retain or reuse the caller-provided buffer.
	http                         bool
	httpTail                     uint32
	off, seq, start, length, pos uint64
	hpos, hlen, ext, keypos      uint8
	bits, code                   byte
	masked                       bool
	key                          [4]byte
	rawHash                      proofHash
	done                         [4]proofGeometry
	// Independent real-framer view. No transport cursor is used to manufacture it.
	frameOff, frameSeq uint64
	frame              proofGeometry
	frameHash          proofHash
	frameN             uint64
}
type proofNetworkReader struct {
	r io.Reader
	p *proofStream
}

func (r *proofNetworkReader) Read(b []byte) (int, error) {
	n, e := r.r.Read(b)
	if n > 0 {
		r.p.transport(b[:n])
	}
	return n, e
}
func newProofStream(http bool) *proofStream {
	id := atomic.AddUint32(&proofIDs, 1)
	if id == 0 {
		atomic.StoreUint32(&proofIDsExhausted, 1)
	}
	p := &proofStream{id: id, http: http}
	p.bad = atomic.LoadUint32(&proofIDsExhausted) != 0
	return p
}
func (p *proofStream) increment(v *uint64) {
	if *v == ^uint64(0) {
		p.bad = true
		return
	}
	*v++
}
func (p *proofStream) transport(b []byte) {
	for _, v := range b {
		if p.http {
			p.httpTail = (p.httpTail << 8) | uint32(v)
			if p.httpTail == 0x0d0a0d0a {
				p.http = false
			}
			continue
		}
		if p.hpos == 0 {
			p.start = p.off
			p.increment(&p.seq)
			p.bits = v
			p.hpos = 1
			p.increment(&p.off)
			continue
		}
		if p.hpos == 1 {
			p.code = v & 127
			p.masked = v&128 != 0
			p.length = uint64(p.code)
			p.ext = 0
			if p.code == 126 {
				p.ext = 2
				p.length = 0
			}
			if p.code == 127 {
				p.ext = 8
				p.length = 0
			}
			p.hlen = 2 + p.ext
			if p.masked {
				p.hlen += 4
			}
			p.hpos = 2
			p.keypos = 0
		} else if p.ext > 0 {
			// Match the pinned parser's high-bit clearing, but mark it ambiguous.
			if p.code == 127 && p.ext == 8 && v&128 != 0 {
				p.bad = true
				v &= 127
			}
			p.length = (p.length << 8) | uint64(v)
			p.ext--
			p.hpos++
		} else if p.masked && p.keypos < 4 {
			p.key[p.keypos] = v
			p.keypos++
			p.hpos++
		} else {
			if p.masked {
				v ^= p.key[p.pos%4]
			}
			p.rawHash.byte(v)
			p.pos++
		}
		p.increment(&p.off)
		if p.hpos == p.hlen && p.pos == 0 {
			p.rawHash.reset()
		}
		if p.hpos == p.hlen && p.pos == p.length {
			g := proofGeometry{Seq: p.seq, Start: p.start, Length: p.length, End: p.off, Header: p.hlen, Bits: p.bits, Code: p.code, Mask: p.masked, Sum: p.rawHash.sum()}
			p.done[p.seq%4] = g
			p.hpos = 0
			p.pos = 0
		}
	}
}
func (p *proofStream) beginFrame(header []byte, length uint64) {
	if p == nil {
		return
	}
	p.increment(&p.frameSeq)
	p.frame = proofGeometry{Seq: p.frameSeq, Start: p.frameOff, Header: uint8(len(header)), Length: length, Bits: header[0], Code: header[1] & 127, Mask: header[1]&128 != 0}
	p.frameOff += uint64(len(header))
	if p.frameOff < p.frame.Start {
		p.bad = true
	}
	p.frameN = 0
	p.frameHash.reset()
}
func (p *proofStream) readFrame(b []byte) {
	if p == nil {
		return
	}
	p.frameHash.write(b)
	p.frameN += uint64(len(b))
	old := p.frameOff
	p.frameOff += uint64(len(b))
	if p.frameOff < old {
		p.bad = true
	}
}

// FrameProof contains snapshots, not borrowed payload pointers. Its stream
// reference is used only synchronously under rio to latch first emission.
type FrameProof struct {
	stream            *proofStream
	raw, frame        proofGeometry
	full, first       [8]uint32
	fullN, firstN     uint64
	opcode            byte
	covered, observed bool
}

func frameProof(frame frameReader, data []byte, opcode byte) FrameProof {
	hf, ok := frame.(*hybiFrameReader)
	if !ok || hf.proof == nil {
		return FrameProof{}
	}
	p := hf.proof
	f := p.frame
	f.End = p.frameOff
	f.Sum = p.frameHash.sum()
	raw := p.done[f.Seq%4]
	return FrameProof{stream: p, raw: raw, frame: f, full: proofSum(data), fullN: uint64(len(data)), opcode: opcode, covered: !p.bad && raw.Seq == f.Seq && p.frameN == f.Length}
}

// ObserveFirst must occur immediately before the first validation Unmarshal,
// while the existing global reservation lease and per-connection rio are held.
func (p *FrameProof) ObserveFirst(data []byte) {
	if p != nil && p.stream != nil {
		p.first = proofSum(data)
		p.firstN = uint64(len(data))
		p.observed = true
	}
}
func emitProofHash(label string, h [8]uint32) {
	println("WSP1", label, h[0], h[1], h[2], h[3], h[4], h[5], h[6], h[7])
}
func emitProofGeometry(label string, g proofGeometry) {
	println("WSP1", label, g.Seq, g.Start, g.Header, g.Length, g.End, uint32(g.Bits), uint32(g.Code), g.Mask)
}
func (p *FrameProof) EmitFailure() {
	if p == nil || p.stream == nil || p.stream.emitted {
		return
	}
	p.stream.emitted = true
	println("WSP1 INIT", p.stream.id, p.frame.Seq, 1, p.covered, p.observed)
	emitProofGeometry("RAW", p.raw)
	emitProofHash("RAWHASH", p.raw.Sum)
	emitProofGeometry("FRAME", p.frame)
	emitProofHash("FRAMEHASH", p.frame.Sum)
	println("WSP1 FULL", p.fullN, uint32(p.opcode))
	emitProofHash("FULLHASH", p.full)
	println("WSP1 FIRST", p.firstN)
	emitProofHash("FIRSTHASH", p.first)
	println("WSP1 FAIL", p.stream.id, p.frame.Seq)
}
