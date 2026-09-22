package model

import "math"

// ContactMatrix is a per-group contact rate matrix C where C[a][b] is the
// daily rate of contacts that one member of group a has with members of
// group b (grouped paradigm). Only non-negative finite values are allowed;
// rows do not need to close (contact rates are not probabilities).
type ContactMatrix [][]float64

// MigrationMatrix is a per-patch flow matrix M for the metapopulation
// paradigm. Off-diagonal M[dst][src] is the per-capita daily flow from src
// to dst and must be non-negative; the diagonal is derived at runtime as the
// remaining "stay" fraction. For each source column the total outflow
// (sum over dst != src) must not exceed 1, otherwise a step could remove
// more people than the patch holds.
type MigrationMatrix [][]float64

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

// ValidateContacts checks shape (n x n), finiteness and non-negativity.
func ValidateContacts(c ContactMatrix, n int) error {
	if len(c) != n {
		return validationError(ReasonContactShape,
			"contact matrix has %d rows, want %d", len(c), n)
	}
	for i := range c {
		if len(c[i]) != n {
			return validationError(ReasonMatrixNotSquare,
				"contact matrix row %d has %d columns, want %d", i, len(c[i]), n)
		}
		for j, v := range c[i] {
			if !isFinite(v) {
				return validationError(ReasonNonFinite,
					"contact matrix C[%d][%d] is not finite", i, j)
			}
			if v < 0 {
				return validationError(ReasonNegativeValue,
					"contact matrix C[%d][%d] = %g is negative", i, j, v)
			}
		}
	}
	return nil
}

// ValidateMigration checks shape (n x n), finiteness, non-negativity and the
// per-source outflow <= 1 constraint. Diagonal entries are ignored (they are
// derived) but must still be finite.
func ValidateMigration(m MigrationMatrix, n int) error {
	if len(m) != n {
		return validationError(ReasonMigrationShape,
			"migration matrix has %d rows, want %d", len(m), n)
	}
	for i := range m {
		if len(m[i]) != n {
			return validationError(ReasonMatrixNotSquare,
				"migration matrix row %d has %d columns, want %d", i, len(m[i]), n)
		}
		for j, v := range m[i] {
			if !isFinite(v) {
				return validationError(ReasonNonFinite,
					"migration matrix M[%d][%d] is not finite", i, j)
			}
			if v < 0 {
				return validationError(ReasonNegativeValue,
					"migration matrix M[%d][%d] = %g is negative", i, j, v)
			}
		}
	}
	for src := 0; src < n; src++ {
		var outflow float64
		for dst := 0; dst < n; dst++ {
			if dst != src {
				outflow += m[dst][src]
			}
		}
		if outflow > 1+1e-9 {
			return validationError(ReasonMigrationOutflow,
				"migration outflows from patch %d sum to %.6f > 1", src, outflow)
		}
	}
	return nil
}
