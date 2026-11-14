// Package sqliteuri builds the file: URIs the SQLite driver opens.
//
// In a file: URI '%' starts an escape, '?' the query and '#' a fragment,
// so a path holding one, pasted in as it is, opens another file or fails.
// Every SQLite file is opened through File so a path names the file it
// names.
package sqliteuri

import "strings"

var escaper = strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23")

// Path is path escaped for the path part of a file: URI.
func Path(path string) string { return escaper.Replace(path) }

// File is the file: URI of path with the query query (already encoded,
// without the '?'); an empty query adds none.
func File(path, query string) string {
	if query == "" {
		return "file:" + Path(path)
	}
	return "file:" + Path(path) + "?" + query
}
