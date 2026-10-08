package team

import "math/bits"

// Exact linear algebra for the disclosure check (release.go), over the
// prime field GF(2^61-1). The matrices have entries 0 and ±1, so their rank
// over this field equals their rank over the rationals unless the prime
// divides every maximal minor of a submatrix, which for matrices of a few
// hundred columns is not a practical concern. Floating point would need a
// tolerance instead; exact arithmetic has none to get wrong.

const gfP = (uint64(1) << 61) - 1

func gfReduce(x uint64) uint64 {
	x = (x & gfP) + (x >> 61)
	if x >= gfP {
		x -= gfP
	}
	return x
}

func gfMul(a, b uint64) uint64 {
	hi, lo := bits.Mul64(a, b)
	// a, b < 2^61, so the product is below 2^122: fold the high bits twice.
	x := (lo & gfP) + ((lo >> 61) | (hi << 3))
	return gfReduce(x)
}

func gfAdd(a, b uint64) uint64 { return gfReduce(a + b) }

func gfSub(a, b uint64) uint64 { return gfReduce(a + gfP - b) }

func gfNeg(a uint64) uint64 { return gfSub(0, a) }

func gfInv(a uint64) uint64 {
	// Fermat: a^(p-2).
	result, base, e := uint64(1), a, gfP-2
	for e > 0 {
		if e&1 == 1 {
			result = gfMul(result, base)
		}
		base = gfMul(base, base)
		e >>= 1
	}
	return result
}

// gfRREF reduces rows (each of length n) to reduced row echelon form in
// place and returns the pivot column of each non-zero row, in order.
// Rows past len(pivots) are zero afterwards.
func gfRREF(rows [][]uint64, n int) []int {
	var pivots []int
	r := 0
	for col := 0; col < n && r < len(rows); col++ {
		sel := -1
		for i := r; i < len(rows); i++ {
			if rows[i][col] != 0 {
				sel = i
				break
			}
		}
		if sel < 0 {
			continue
		}
		rows[r], rows[sel] = rows[sel], rows[r]
		inv := gfInv(rows[r][col])
		for j := col; j < n; j++ {
			rows[r][j] = gfMul(rows[r][j], inv)
		}
		for i := range rows {
			if i == r || rows[i][col] == 0 {
				continue
			}
			f := rows[i][col]
			for j := col; j < n; j++ {
				if rows[r][j] != 0 {
					rows[i][j] = gfSub(rows[i][j], gfMul(f, rows[r][j]))
				}
			}
		}
		pivots = append(pivots, col)
		r++
	}
	return pivots
}

// gfComplement describes the orthogonal complement of a row space: for each
// of the n columns j, the vector nu[j] of its coordinates in a basis of
// the space's null space. A vector f supported on a column set S lies in
// the row space exactly when sum f_j nu[j] = 0 over j in S, so
//
//	dim(rowspace ∩ span{e_j : j in S}) = |S| - rank{nu[j] : j in S}.
func gfComplement(rows [][]uint64, n int) [][]uint64 {
	m := make([][]uint64, len(rows))
	for i := range rows {
		m[i] = append([]uint64(nil), rows[i]...)
	}
	pivots := gfRREF(m, n)
	isPivot := make([]int, n)
	for i := range isPivot {
		isPivot[i] = -1
	}
	for i, c := range pivots {
		isPivot[c] = i
	}
	var free []int
	for j := 0; j < n; j++ {
		if isPivot[j] < 0 {
			free = append(free, j)
		}
	}
	nu := make([][]uint64, n)
	for j := 0; j < n; j++ {
		v := make([]uint64, len(free))
		if r := isPivot[j]; r >= 0 {
			for fi, f := range free {
				v[fi] = gfNeg(m[r][f])
			}
		}
		nu[j] = v
	}
	for fi, f := range free {
		nu[f][fi] = 1
	}
	return nu
}

// gfRank is the rank of a set of vectors.
func gfRank(vecs [][]uint64) int {
	if len(vecs) == 0 {
		return 0
	}
	n := len(vecs[0])
	m := make([][]uint64, len(vecs))
	for i := range vecs {
		m[i] = append([]uint64(nil), vecs[i]...)
	}
	return len(gfRREF(m, n))
}
