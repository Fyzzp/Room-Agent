// Package crypto 提供主控与 Agent 之间的安全通信加密
// 基于 Ed25519 长期身份密钥 + X25519 ECDH 密钥交换 + AES-256-GCM 会话加密
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

const (
	envelopeVersion = 0x01
	envelopeHeader  = 1 + 8 // version(1) + seq(8)
	gcmTagSize      = 16
	nonceSize       = 12
	windowSize      = 64
)

// Identity 持有 Ed25519 长期签名密钥对
type Identity struct {
	PrivateKey ed25519.PrivateKey
	PublicKey  ed25519.PublicKey
}

// GenerateIdentity 生成新的 Ed25519 身份密钥对
func GenerateIdentity() (*Identity, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ed25519 key: %w", err)
	}
	return &Identity{PrivateKey: priv, PublicKey: pub}, nil
}

// PublicKeyBase64 返回 Base64 编码的公钥
func (id *Identity) PublicKeyBase64() string {
	return base64.StdEncoding.EncodeToString(id.PublicKey)
}

// ParsePublicKey 从 Base64 字符串解析公钥
func ParsePublicKey(b64 string) (ed25519.PublicKey, error) {
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("decode public key: %w", err)
	}
	if len(data) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("invalid public key size: %d", len(data))
	}
	return ed25519.PublicKey(data), nil
}

// GenerateEphemeral 生成 X25519 临时密钥对
func GenerateEphemeral() (priv, pub []byte, err error) {
	priv = make([]byte, 32)
	if _, err = rand.Read(priv); err != nil {
		return nil, nil, err
	}
	pub, err = curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		return nil, nil, err
	}
	return priv, pub, nil
}

// ComputeSharedSecret 执行 X25519 ECDH 计算共享密钥
func ComputeSharedSecret(myPriv, theirPub []byte) ([]byte, error) {
	return curve25519.X25519(myPriv, theirPub)
}

// Sign 使用 Ed25519 私钥签名数据
func Sign(privKey ed25519.PrivateKey, data []byte) []byte {
	return ed25519.Sign(privKey, data)
}

// Verify 使用 Ed25519 公钥验证签名
func Verify(pubKey ed25519.PublicKey, data, sig []byte) bool {
	return ed25519.Verify(pubKey, data, sig)
}

// Session 持有 AES-256-GCM 双向加密会话
type Session struct {
	sendCipher cipher.AEAD
	recvCipher cipher.AEAD
	sendSeq    atomic.Uint64
	sendNonce  [nonceSize]byte
	recvNonce  [nonceSize]byte

	recvMu     sync.Mutex
	recvWindow replayWindow
}

// replayWindow 滑动窗口防重放
type replayWindow struct {
	maxSeq uint64
	bitmap uint64
}

func (w *replayWindow) Check(seq uint64) bool {
	if seq == 0 {
		return false
	}
	if seq > w.maxSeq {
		shift := seq - w.maxSeq
		if shift >= windowSize {
			w.bitmap = 0
		} else {
			w.bitmap <<= shift
		}
		w.maxSeq = seq
		w.bitmap |= 1
		return true
	}
	diff := w.maxSeq - seq
	if diff >= windowSize {
		return false
	}
	bit := uint64(1) << diff
	if w.bitmap&bit != 0 {
		return false
	}
	w.bitmap |= bit
	return true
}

// DeriveSession 从共享密钥派生双向 AES-256-GCM 会话密钥
// isMaster 决定发送/接收密钥方向
func DeriveSession(sharedSecret, agentEphPub, masterEphPub []byte, isMaster bool) (*Session, error) {
	salt := make([]byte, 0, 64)
	salt = append(salt, agentEphPub...)
	salt = append(salt, masterEphPub...)

	hk := hkdf.New(sha256.New, sharedSecret, salt, []byte("xray-panel-v1"))

	var keys [4][]byte
	keys[0] = make([]byte, 32) // m2a key
	keys[1] = make([]byte, 32) // a2m key
	keys[2] = make([]byte, nonceSize) // m2a nonce
	keys[3] = make([]byte, nonceSize) // a2m nonce

	for i := range keys {
		if _, err := io.ReadFull(hk, keys[i]); err != nil {
			return nil, fmt.Errorf("hkdf read: %w", err)
		}
	}

	m2aKey, a2mKey := keys[0], keys[1]
	m2aNonce, a2mNonce := keys[2], keys[3]

	var sendKey, recvKey []byte
	var sendNonce, recvNonce [nonceSize]byte

	if isMaster {
		sendKey, recvKey = m2aKey, a2mKey
		copy(sendNonce[:], m2aNonce)
		copy(recvNonce[:], a2mNonce)
	} else {
		sendKey, recvKey = a2mKey, m2aKey
		copy(sendNonce[:], a2mNonce)
		copy(recvNonce[:], m2aNonce)
	}

	sendBlock, err := aes.NewCipher(sendKey)
	if err != nil {
		return nil, err
	}
	sendCipher, err := cipher.NewGCM(sendBlock)
	if err != nil {
		return nil, err
	}

	recvBlock, err := aes.NewCipher(recvKey)
	if err != nil {
		return nil, err
	}
	recvCipher, err := cipher.NewGCM(recvBlock)
	if err != nil {
		return nil, err
	}

	return &Session{
		sendCipher: sendCipher,
		recvCipher: recvCipher,
		sendNonce:  sendNonce,
		recvNonce:  recvNonce,
	}, nil
}

// Encrypt 加密明文，输出: [version(1)][seq(8)][ciphertext+gcmTag(16)]
func (s *Session) Encrypt(plaintext []byte) ([]byte, error) {
	seq := s.sendSeq.Add(1)
	nonce := makeNonce(s.sendNonce, seq)
	ciphertext := s.sendCipher.Seal(nil, nonce[:], plaintext, nil)

	out := make([]byte, envelopeHeader+len(ciphertext))
	out[0] = envelopeVersion
	binary.BigEndian.PutUint64(out[1:9], seq)
	copy(out[envelopeHeader:], ciphertext)
	return out, nil
}

// Decrypt 解密信封，返回明文
func (s *Session) Decrypt(envelope []byte) ([]byte, error) {
	if len(envelope) < envelopeHeader+gcmTagSize {
		return nil, errors.New("envelope too short")
	}
	if envelope[0] != envelopeVersion {
		return nil, fmt.Errorf("unknown envelope version: %d", envelope[0])
	}

	seq := binary.BigEndian.Uint64(envelope[1:9])

	s.recvMu.Lock()
	ok := s.recvWindow.Check(seq)
	s.recvMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("replay or out-of-window seq: %d", seq)
	}

	nonce := makeNonce(s.recvNonce, seq)
	plaintext, err := s.recvCipher.Open(nil, nonce[:], envelope[envelopeHeader:], nil)
	if err != nil {
		return nil, fmt.Errorf("decrypt: %w", err)
	}
	return plaintext, nil
}

func makeNonce(base [nonceSize]byte, seq uint64) [nonceSize]byte {
	var nonce [nonceSize]byte
	copy(nonce[:], base[:])
	var seqBytes [nonceSize]byte
	binary.BigEndian.PutUint64(seqBytes[4:], seq)
	for i := range nonce {
		nonce[i] ^= seqBytes[i]
	}
	return nonce
}
