// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package argon2 implements the key derivation function Argon2, with extra parameters supported.
// This is a fork of the standard library x/crypto/argon2. It implements the key derivation function
// Argon2. Argon2 was selected as the winner of the Password Hashing Competition and can be used to
// derive cryptographic keys from passwords.
//
// For a detailed specification of Argon2 see [1].
//
//  1. https://github.com/P-H-C/phc-winner-argon2/blob/master/argon2-specs.pdf
//  2. https://tools.ietf.org/html/draft-irtf-cfrg-argon2-03#section-9.3
//  3. https://cheatsheetseries.owasp.org/cheatsheets/Password_Storage_Cheat_Sheet.html#argon2id
//  4. https://eprint.iacr.org/2016/759.pdf
package argon2

import (
	"encoding/binary"
	"sync"

	"golang.org/x/crypto/blake2b"
)

// The Argon2 version implemented by this package.
const Version = 0x13

// Argon2d maximizes resistance to GPU cracking attacks. It accesses the memory array in a password
// dependent order, which reduces the possibility of time–memory trade-off (TMTO) attacks, but
// introduces possible side-channel attacks.
const Argon2d = 0

// Argon2i is the side-channel resistant version of Argon2. It uses data-independent memory access,
// which is preferred for password hashing and password-based key derivation. Argon2i requires more
// passes over memory than Argon2id to protect from trade-off attacks. Based on cryptanalysis from
// 2016 (see [4]), time > 10 (iterations) are required for 1GB of memory. For interactive password
// prompts where memory is constrained, this mode will be too slow to use. Argon2id is recommended
// instead.
const Argon2i = 1

// Argon2id is a hybrid version of Argon2 combining Argon2i and Argon2d. It is recommended[3] by
// OWASP for normal password hashing. It uses data-independent memory access for the first half of
// the first iteration over the memory and data-dependent memory access for the rest. Argon2id is
// side-channel resistant and provides better brute- force cost savings due to time-memory tradeoffs
// than Argon2i
const Argon2id = 2

// DeriveKey derives a key from password, salt, secret (a.k.a key or pepper), data, and cost
// parameters. The mode is one of Argon2d, Argon2i, or Argon2id. You may pass nil for any of salt,
// secret, or data to exclude them from the hash. A byte slice of length keyLen that can be used as
// cryptographic key. The CPU cost and parallelism degree must be greater than zero.
//
// For example, you can get a derived key for e.g. AES-256 (which needs a
// 32-byte key) by doing:
//
//	key := argon2.DeriveKey(argon2.Argon2id, []byte("some password"), salt, nil, nil, 1, 64*1024, 4, 32)
//
// OWASP publishes recommendations[3] for sensible cost parameters for password hashing. The
// original draft RFC also contains recommendations, though they may not be up-to-date. For normal
// password hashing, a salt, secret, and key of length 16 bytes is sufficient.
//
// The time parameter specifies the number of passes over the memory and the memory parameter
// specifies the size of the memory in KiB. For example memory=64*1024 sets the memory cost to ~64
// MB. The number of threads can be adjusted to the numbers of available CPUs. The cost parameters
// should be increased as memory latency and CPU parallelism increases. Remember to get a good
// random salt.
func DeriveKey(mode int, password, salt, secret, data []byte, time, memory uint32, threads uint8, keyLen uint32) []byte {
	if time < 1 {
		panic("argon2: number of rounds too small")
	}
	if threads < 1 {
		panic("argon2: parallelism degree too low")
	}
	h0 := initHash(password, salt, secret, data, time, memory, uint32(threads), keyLen, mode)

	memory = memory / (syncPoints * uint32(threads)) * (syncPoints * uint32(threads))
	if memory < 2*syncPoints*uint32(threads) {
		memory = 2 * syncPoints * uint32(threads)
	}
	B := initBlocks(&h0, memory, uint32(threads))
	processBlocks(B, time, memory, uint32(threads), mode)
	return extractKey(B, memory, uint32(threads), keyLen)
}

const (
	blockLength = 128
	syncPoints  = 4
)

type block [blockLength]uint64

func initHash(password, salt, key, data []byte, time, memory, threads, keyLen uint32, mode int) [blake2b.Size + 8]byte {
	var (
		h0     [blake2b.Size + 8]byte
		params [24]byte
		tmp    [4]byte
	)

	b2, _ := blake2b.New512(nil)
	binary.LittleEndian.PutUint32(params[0:4], threads)
	binary.LittleEndian.PutUint32(params[4:8], keyLen)
	binary.LittleEndian.PutUint32(params[8:12], memory)
	binary.LittleEndian.PutUint32(params[12:16], time)
	binary.LittleEndian.PutUint32(params[16:20], uint32(Version))
	binary.LittleEndian.PutUint32(params[20:24], uint32(mode))
	b2.Write(params[:])
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(password)))
	b2.Write(tmp[:])
	b2.Write(password)
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(salt)))
	b2.Write(tmp[:])
	b2.Write(salt)
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(key)))
	b2.Write(tmp[:])
	b2.Write(key)
	binary.LittleEndian.PutUint32(tmp[:], uint32(len(data)))
	b2.Write(tmp[:])
	b2.Write(data)
	b2.Sum(h0[:0])
	return h0
}

func initBlocks(h0 *[blake2b.Size + 8]byte, memory, threads uint32) []block {
	var block0 [1024]byte
	B := make([]block, memory)
	for lane := uint32(0); lane < threads; lane++ {
		j := lane * (memory / threads)
		binary.LittleEndian.PutUint32(h0[blake2b.Size+4:], lane)

		binary.LittleEndian.PutUint32(h0[blake2b.Size:], 0)
		blake2bHash(block0[:], h0[:])
		for i := range B[j+0] {
			B[j+0][i] = binary.LittleEndian.Uint64(block0[i*8:])
		}

		binary.LittleEndian.PutUint32(h0[blake2b.Size:], 1)
		blake2bHash(block0[:], h0[:])
		for i := range B[j+1] {
			B[j+1][i] = binary.LittleEndian.Uint64(block0[i*8:])
		}
	}
	return B
}

func processBlocks(B []block, time, memory, threads uint32, mode int) {
	lanes := memory / threads
	segments := lanes / syncPoints

	processSegment := func(n, slice, lane uint32, wg *sync.WaitGroup) {
		var addresses, in, zero block
		if mode == Argon2i || (mode == Argon2id && n == 0 && slice < syncPoints/2) {
			in[0] = uint64(n)
			in[1] = uint64(lane)
			in[2] = uint64(slice)
			in[3] = uint64(memory)
			in[4] = uint64(time)
			in[5] = uint64(mode)
		}

		index := uint32(0)
		if n == 0 && slice == 0 {
			index = 2 // we have already generated the first two blocks
			if mode == Argon2i || mode == Argon2id {
				in[6]++
				processBlock(&addresses, &in, &zero)
				processBlock(&addresses, &addresses, &zero)
			}
		}

		offset := lane*lanes + slice*segments + index
		var random uint64
		for index < segments {
			prev := offset - 1
			if index == 0 && slice == 0 {
				prev += lanes // last block in lane
			}
			if mode == Argon2i || (mode == Argon2id && n == 0 && slice < syncPoints/2) {
				if index%blockLength == 0 {
					in[6]++
					processBlock(&addresses, &in, &zero)
					processBlock(&addresses, &addresses, &zero)
				}
				random = addresses[index%blockLength]
			} else {
				random = B[prev][0]
			}
			newOffset := indexAlpha(random, lanes, segments, threads, n, slice, lane, index)
			processBlockXOR(&B[offset], &B[prev], &B[newOffset])
			index, offset = index+1, offset+1
		}
		wg.Done()
	}

	for n := uint32(0); n < time; n++ {
		for slice := uint32(0); slice < syncPoints; slice++ {
			var wg sync.WaitGroup
			for lane := uint32(0); lane < threads; lane++ {
				wg.Add(1)
				go processSegment(n, slice, lane, &wg)
			}
			wg.Wait()
		}
	}

}

func extractKey(B []block, memory, threads, keyLen uint32) []byte {
	lanes := memory / threads
	for lane := uint32(0); lane < threads-1; lane++ {
		for i, v := range B[(lane*lanes)+lanes-1] {
			B[memory-1][i] ^= v
		}
	}

	var block [1024]byte
	for i, v := range B[memory-1] {
		binary.LittleEndian.PutUint64(block[i*8:], v)
	}
	key := make([]byte, keyLen)
	blake2bHash(key, block[:])
	return key
}

func indexAlpha(rand uint64, lanes, segments, threads, n, slice, lane, index uint32) uint32 {
	refLane := uint32(rand>>32) % threads
	if n == 0 && slice == 0 {
		refLane = lane
	}
	m, s := 3*segments, ((slice+1)%syncPoints)*segments
	if lane == refLane {
		m += index
	}
	if n == 0 {
		m, s = slice*segments, 0
		if slice == 0 || lane == refLane {
			m += index
		}
	}
	if index == 0 || lane == refLane {
		m--
	}
	return phi(rand, uint64(m), uint64(s), refLane, lanes)
}

func phi(rand, m, s uint64, lane, lanes uint32) uint32 {
	p := rand & 0xFFFFFFFF
	p = (p * p) >> 32
	p = (p * m) >> 32
	return lane*lanes + uint32((s+m-(p+1))%uint64(lanes))
}
