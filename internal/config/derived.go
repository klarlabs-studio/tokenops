package config

import (
	"fmt"
	"time"
)

// Derived figures' max age (storage.derived_max_age, ADR 0019).
const (
	// DefaultDerivedMaxAge is how long a derived figure is answered while
	// nothing it reads changed. What it shows that moves with the clock
	// alone (a reset counting down, a rolling window easing, a reading
	// turning stale) lags by at most this much.
	DefaultDerivedMaxAge = 5 * time.Minute
	// MaxDerivedMaxAge is the longest allowed: a glance whose countdowns
	// are more than an hour behind is no longer the present one.
	MaxDerivedMaxAge = time.Hour
)

// EffectiveDerivedMaxAge is storage.derived_max_age with its default.
func (s StorageConfig) EffectiveDerivedMaxAge() time.Duration {
	if s.DerivedMaxAge <= 0 {
		return DefaultDerivedMaxAge
	}
	return s.DerivedMaxAge
}

func (s StorageConfig) validateDerivedMaxAge() error {
	if s.DerivedMaxAge < 0 || s.DerivedMaxAge > MaxDerivedMaxAge {
		return fmt.Errorf("storage.derived_max_age must be between 0 (the default, %s) and %s, got %s",
			DefaultDerivedMaxAge, MaxDerivedMaxAge, s.DerivedMaxAge)
	}
	return nil
}
