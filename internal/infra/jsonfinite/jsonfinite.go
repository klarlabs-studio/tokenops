// Package jsonfinite makes a value safe for encoding/json, which refuses
// NaN and ±Inf and fails the whole document over one of them.
//
// A stored analysis is dozens of derived figures. One that comes out as
// 0/0 is a bug in that figure, and it must be fixed where it is computed,
// but it should cost that figure, not every figure beside it: losing the
// whole document left the daemon answering from a stale analysis with
// nothing on any surface saying why.
package jsonfinite

import (
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Sanitize zeroes every NaN or ±Inf float reachable from ptr, which must
// be a pointer, and returns the JSON path of each one it zeroed (nil when
// there were none) so the caller can name them. It follows structs,
// pointers, interfaces, slices, arrays and string-keyed maps, and skips
// unexported and `json:"-"` fields, which encoding/json never writes.
// Slices and maps are fixed in place, so a caller sharing them with ptr
// sees the zeroes too.
func Sanitize(ptr any) []string {
	v := reflect.ValueOf(ptr)
	if !v.IsValid() || v.Kind() != reflect.Pointer || v.IsNil() {
		return nil
	}
	var paths []string
	walk(v.Elem(), "", &paths)
	return paths
}

func walk(v reflect.Value, path string, paths *[]string) {
	switch v.Kind() {
	case reflect.Float32, reflect.Float64:
		if f := v.Float(); math.IsNaN(f) || math.IsInf(f, 0) {
			v.SetFloat(0)
			*paths = append(*paths, path)
		}
	case reflect.Pointer:
		if !v.IsNil() {
			walk(v.Elem(), path, paths)
		}
	case reflect.Interface:
		walkInterface(v, path, paths)
	case reflect.Struct:
		walkStruct(v, path, paths)
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walk(v.Index(i), path+"["+strconv.Itoa(i)+"]", paths)
		}
	case reflect.Map:
		walkMap(v, path, paths)
	}
}

// walkInterface fixes what an interface holds. A pointer inside is fixed
// through; a value inside is not addressable, so it is copied, fixed and
// stored back.
func walkInterface(v reflect.Value, path string, paths *[]string) {
	if v.IsNil() {
		return
	}
	inner := v.Elem()
	if inner.Kind() == reflect.Pointer {
		walk(inner, path, paths)
		return
	}
	cp := reflect.New(inner.Type()).Elem()
	cp.Set(inner)
	before := len(*paths)
	walk(cp, path, paths)
	if len(*paths) > before && v.CanSet() {
		v.Set(cp)
	}
}

func walkStruct(v reflect.Value, path string, paths *[]string) {
	t := v.Type()
	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Name
		if tag, ok := f.Tag.Lookup("json"); ok {
			n, _, _ := strings.Cut(tag, ",")
			if n == "-" {
				continue
			}
			if n != "" {
				name = n
			}
		}
		walk(v.Field(i), join(path, name), paths)
	}
}

// walkMap visits keys in order so the paths come back the same every run.
// Map values are not addressable: each is copied, fixed and stored back.
func walkMap(v reflect.Value, path string, paths *[]string) {
	if v.IsNil() || v.Type().Key().Kind() != reflect.String {
		return
	}
	keys := v.MapKeys()
	sort.Slice(keys, func(i, j int) bool { return keys[i].String() < keys[j].String() })
	for _, k := range keys {
		cp := reflect.New(v.Type().Elem()).Elem()
		cp.Set(v.MapIndex(k))
		before := len(*paths)
		walk(cp, join(path, k.String()), paths)
		if len(*paths) > before {
			v.SetMapIndex(k, cp)
		}
	}
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}
