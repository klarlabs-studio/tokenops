package sqlite

import (
	"testing"
	"time"
)

func TestNullStringEmpty(t *testing.T) {
	got := nullString("")
	if got.Valid {
		t.Error("nullString('') should be invalid")
	}
}

func TestNullStringNonEmpty(t *testing.T) {
	got := nullString("hello")
	if !got.Valid || got.String != "hello" {
		t.Errorf("nullString('hello') = %+v", got)
	}
}

func TestNullInt64IfNonZeroZero(t *testing.T) {
	got := nullInt64IfNonZero(0)
	if got.Valid {
		t.Error("nullInt64IfNonZero(0) should be invalid")
	}
}

func TestNullInt64IfNonZeroNonZero(t *testing.T) {
	got := nullInt64IfNonZero(42)
	if !got.Valid || got.Int64 != 42 {
		t.Errorf("nullInt64IfNonZero(42) = %+v", got)
	}
}

func TestNullFloat64IfNonZeroZero(t *testing.T) {
	got := nullFloat64IfNonZero(0)
	if got.Valid {
		t.Error("nullFloat64IfNonZero(0) should be invalid")
	}
}

func TestNullFloat64IfNonZeroNonZero(t *testing.T) {
	got := nullFloat64IfNonZero(3.14)
	if !got.Valid || got.Float64 != 3.14 {
		t.Errorf("nullFloat64IfNonZero(3.14) = %+v", got)
	}
}

func TestTimeFromNSEpoch(t *testing.T) {
	got := timeFromNS(0)
	if !got.Equal(time.Unix(0, 0).UTC()) {
		t.Errorf("timeFromNS(0) = %v", got)
	}
}

func TestTimeFromNSKnown(t *testing.T) {
	got := timeFromNS(1_000_000_000)
	want := time.Unix(1, 0).UTC()
	if !got.Equal(want) {
		t.Errorf("timeFromNS(1000000000) = %v, want %v", got, want)
	}
}

func TestNullStringSQLNullString(t *testing.T) {
	_ = nullString("x")
}

func TestNullInt64SQLNullInt64(t *testing.T) {
	_ = nullInt64IfNonZero(1)
}

func TestNullFloat64SQLNullFloat64(t *testing.T) {
	_ = nullFloat64IfNonZero(1)
}
