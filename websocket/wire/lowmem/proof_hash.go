package websocket

// SHA-256, FIPS 180-4. Private value-state implementation: no payload retention
// outside the algorithm's 64-byte partial block, no interface or heap buffer.
// This is evidence fingerprinting, not an authentication primitive.
import "math/bits"

type proofHash struct { H [8]uint32; B [64]byte; N uint64; Used uint8 }
func (d *proofHash) reset() { *d=proofHash{H:[8]uint32{0x6a09e667,0xbb67ae85,0x3c6ef372,0xa54ff53a,0x510e527f,0x9b05688c,0x1f83d9ab,0x5be0cd19}} }
var proofK=[64]uint32{0x428a2f98,0x71374491,0xb5c0fbcf,0xe9b5dba5,0x3956c25b,0x59f111f1,0x923f82a4,0xab1c5ed5,0xd807aa98,0x12835b01,0x243185be,0x550c7dc3,0x72be5d74,0x80deb1fe,0x9bdc06a7,0xc19bf174,0xe49b69c1,0xefbe4786,0x0fc19dc6,0x240ca1cc,0x2de92c6f,0x4a7484aa,0x5cb0a9dc,0x76f988da,0x983e5152,0xa831c66d,0xb00327c8,0xbf597fc7,0xc6e00bf3,0xd5a79147,0x06ca6351,0x14292967,0x27b70a85,0x2e1b2138,0x4d2c6dfc,0x53380d13,0x650a7354,0x766a0abb,0x81c2c92e,0x92722c85,0xa2bfe8a1,0xa81a664b,0xc24b8b70,0xc76c51a3,0xd192e819,0xd6990624,0xf40e3585,0x106aa070,0x19a4c116,0x1e376c08,0x2748774c,0x34b0bcb5,0x391c0cb3,0x4ed8aa4a,0x5b9cca4f,0x682e6ff3,0x748f82ee,0x78a5636f,0x84c87814,0x8cc70208,0x90befffa,0xa4506ceb,0xbef9a3f7,0xc67178f2}
func (d *proofHash) block() {
 var w [64]uint32
 for i:=0;i<16;i++ {j:=i*4;w[i]=uint32(d.B[j])<<24|uint32(d.B[j+1])<<16|uint32(d.B[j+2])<<8|uint32(d.B[j+3])}
 for i:=16;i<64;i++ {a,b:=w[i-15],w[i-2];s0:=bits.RotateLeft32(a,-7)^bits.RotateLeft32(a,-18)^(a>>3);s1:=bits.RotateLeft32(b,-17)^bits.RotateLeft32(b,-19)^(b>>10);w[i]=w[i-16]+s0+w[i-7]+s1}
 a,b,c,e0,e,f,g,h:=d.H[0],d.H[1],d.H[2],d.H[3],d.H[4],d.H[5],d.H[6],d.H[7]
 for i:=0;i<64;i++ {s1:=bits.RotateLeft32(e,-6)^bits.RotateLeft32(e,-11)^bits.RotateLeft32(e,-25);t1:=h+s1+((e&f)^(^e&g))+proofK[i]+w[i];s0:=bits.RotateLeft32(a,-2)^bits.RotateLeft32(a,-13)^bits.RotateLeft32(a,-22);t2:=s0+((a&b)^(a&c)^(b&c));h=g;g=f;f=e;e=e0+t1;e0=c;c=b;b=a;a=t1+t2}
 d.H[0]+=a;d.H[1]+=b;d.H[2]+=c;d.H[3]+=e0;d.H[4]+=e;d.H[5]+=f;d.H[6]+=g;d.H[7]+=h
}
func (d *proofHash) byte(b byte) {d.B[d.Used]=b;d.N++;d.Used++;if d.Used==64 {d.block();d.Used=0}}
func (d *proofHash) write(p []byte) {for _,b:=range p {d.byte(b)}}
func (d proofHash) sum() [8]uint32 {n:=d.N*8;d.byte(0x80);for d.Used!=56 {d.byte(0)};for i:=7;i>=0;i-- {d.byte(byte(n>>uint(i*8)))};return d.H}
func proofSum(p []byte) [8]uint32 {var d proofHash;d.reset();d.write(p);return d.sum()}
