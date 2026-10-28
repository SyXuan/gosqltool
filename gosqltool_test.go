package gosqltool

import (
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var fakeDriverCounter atomic.Int64

type fakeRows struct {
	cols     []string
	colTypes []string
	rows     [][]driver.Value
	idx      int
	errAfter int // index of the Next call that returns an error, -1 for none
	nextErr  error
}

func (r *fakeRows) Columns() []string { return r.cols }

// ColumnTypeDatabaseTypeName (the method of the
// driver.RowsColumnTypeDatabaseTypeName interface) lets tests
// exercise the ColumnTypes-based conversion in RowsToAny/IterAny.
// A column with no entry reports "", which the library treats as
// unknown.
func (r *fakeRows) ColumnTypeDatabaseTypeName(index int) string {
	if r.colTypes != nil && index < len(r.colTypes) {
		return r.colTypes[index]
	}
	return ""
}
func (r *fakeRows) Close() error { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if r.errAfter >= 0 && r.idx == r.errAfter {
		return r.nextErr
	}
	if r.idx >= len(r.rows) {
		return io.EOF
	}
	copy(dest, r.rows[r.idx])
	r.idx++
	return nil
}

type fakeConn struct {
	cols     []string
	colTypes []string
	rows     [][]driver.Value
	errAfter int
	nextErr  error
}

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) {
	return nil, errors.New("fake driver: prepare not supported")
}
func (c *fakeConn) Close() error { return nil }
func (c *fakeConn) Begin() (driver.Tx, error) {
	return nil, errors.New("fake driver: transactions not supported")
}
func (c *fakeConn) Query(query string, args []driver.Value) (driver.Rows, error) {
	return &fakeRows{cols: c.cols, colTypes: c.colTypes, rows: c.rows, errAfter: c.errAfter, nextErr: c.nextErr}, nil
}

type fakeDriver struct{ conn *fakeConn }

func (d *fakeDriver) Open(name string) (driver.Conn, error) { return d.conn, nil }

// openRows builds a *sql.Rows from a fake driver, so tests need no
// real database.
func openRows(t *testing.T, cols []string, rows [][]driver.Value) *sql.Rows {
	t.Helper()
	return openRowsWithErr(t, cols, rows, -1, nil)
}

func openRowsWithTypes(t *testing.T, cols []string, colTypes []string, rows [][]driver.Value) *sql.Rows {
	t.Helper()
	name := fmt.Sprintf("fake-%d", fakeDriverCounter.Add(1))
	sql.Register(name, &fakeDriver{conn: &fakeConn{cols: cols, colTypes: colTypes, rows: rows, errAfter: -1}})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	got, err := db.Query("SELECT 1")
	if err != nil {
		t.Fatalf("db.Query: %v", err)
	}
	t.Cleanup(func() { _ = got.Close() })
	return got
}

func openRowsWithErr(t *testing.T, cols []string, rows [][]driver.Value, errAfter int, nextErr error) *sql.Rows {
	t.Helper()
	name := fmt.Sprintf("fake-%d", fakeDriverCounter.Add(1))
	sql.Register(name, &fakeDriver{conn: &fakeConn{cols: cols, rows: rows, errAfter: errAfter, nextErr: nextErr}})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	got, err := db.Query("SELECT 1")
	if err != nil {
		t.Fatalf("db.Query: %v", err)
	}
	t.Cleanup(func() { _ = got.Close() })
	return got
}

func TestRowsToMapBasic(t *testing.T) {
	rows := openRows(t,
		[]string{"uid", "username", "created"},
		[][]driver.Value{
			{[]byte("11"), []byte("Trump_new"), []byte("2018-03-20T00:00:00Z")},
			{[]byte("13"), []byte("Trump_new"), []byte("2018-03-20T00:00:00Z")},
		})
	got, err := RowsToMap(rows)
	if err != nil {
		t.Fatalf("RowsToMap: %v", err)
	}
	if got[0]["username"] != "Trump_new" || got[0]["uid"] != "11" {
		t.Fatalf("row 0 = %#v", got[0])
	}
	if got[1]["uid"] != "13" {
		t.Fatalf("row 1 = %#v", got[1])
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
}

func TestRowsToMapNullBecomesEmptyString(t *testing.T) {
	rows := openRows(t,
		[]string{"uid", "nickname"},
		[][]driver.Value{{[]byte("1"), nil}})
	got, err := RowsToMap(rows)
	if err != nil {
		t.Fatalf("RowsToMap: %v", err)
	}
	if got[0]["nickname"] != "" {
		t.Fatalf("NULL nickname = %q, want empty string (compat behaviour)", got[0]["nickname"])
	}
}

func TestRowsToSliceKeepsOrder(t *testing.T) {
	rows := openRows(t,
		[]string{"n"},
		[][]driver.Value{{[]byte("a")}, {[]byte("b")}, {[]byte("c")}})
	got, err := RowsToSlice(rows)
	if err != nil {
		t.Fatalf("RowsToSlice: %v", err)
	}
	want := []map[string]string{{"n": "a"}, {"n": "b"}, {"n": "c"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestRowsToAnyPreservesTypesAndNull(t *testing.T) {
	when := time.Date(2018, 3, 20, 0, 0, 0, 0, time.UTC)
	rows := openRows(t,
		[]string{"id", "score", "active", "name", "when", "note"},
		[][]driver.Value{{int64(1), float64(9.5), true, []byte("x"), when, nil}})
	got, err := RowsToAny(rows)
	if err != nil {
		t.Fatalf("RowsToAny: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	row := got[0]
	if row["id"] != int64(1) || row["score"] != float64(9.5) || row["active"] != true {
		t.Fatalf("typed values = %#v", row)
	}
	if row["name"] != "x" {
		t.Fatalf("[]byte name = %#v, want string \"x\"", row["name"])
	}
	if row["when"] != when {
		t.Fatalf("when = %#v, want %v", row["when"], when)
	}
	if row["note"] != nil {
		t.Fatalf("NULL note = %#v, want nil", row["note"])
	}
}

func TestRowsToJSON(t *testing.T) {
	when := time.Date(2018, 3, 20, 0, 0, 0, 0, time.UTC)
	rows := openRows(t,
		[]string{"uid", "username", "created", "note"},
		[][]driver.Value{{int64(1), []byte("Trump"), when, nil}})
	got, err := RowsToJSON(rows)
	if err != nil {
		t.Fatalf("RowsToJSON: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(got), &decoded); err != nil {
		t.Fatalf("JSON %q does not decode: %v", got, err)
	}
	if len(decoded) != 1 || decoded[0]["uid"] != float64(1) || decoded[0]["username"] != "Trump" ||
		decoded[0]["created"] != "2018-03-20T00:00:00Z" || decoded[0]["note"] != nil {
		t.Fatalf("decoded = %#v", decoded)
	}

	empty := openRows(t, []string{"uid"}, nil)
	got, err = RowsToJSON(empty)
	if err != nil {
		t.Fatalf("RowsToJSON empty: %v", err)
	}
	if got != "[]" {
		t.Fatalf("empty JSON = %q, want \"[]\"", got)
	}
}

func TestRowsToXMLDefaultAndCustomNames(t *testing.T) {
	rows := openRows(t,
		[]string{"uid", "username"},
		[][]driver.Value{{[]byte("1"), []byte("Trump")}})
	got, err := RowsToXML(rows)
	if err != nil {
		t.Fatalf("RowsToXML: %v", err)
	}
	if !strings.Contains(got, "<RowData>") || !strings.Contains(got, "<Row_0>") ||
		!strings.Contains(got, "<uid>1</uid>") || !strings.Contains(got, "</RowData>") {
		t.Fatalf("default XML = %q", got)
	}

	rows = openRows(t,
		[]string{"uid"},
		[][]driver.Value{{[]byte("2")}})
	got, err = RowsToXMLWithNames(rows, "Table", "Row")
	if err != nil {
		t.Fatalf("RowsToXMLWithNames: %v", err)
	}
	if !strings.Contains(got, "<Table>") || !strings.Contains(got, "<Row0>") || !strings.Contains(got, "</Table>") {
		t.Fatalf("custom XML = %q", got)
	}
}

func TestRowsToXMLSanitizesNamesAndEscapesValues(t *testing.T) {
	rows := openRows(t,
		[]string{"user name", "2fa", "uid"},
		[][]driver.Value{{[]byte("a"), []byte("b"), []byte("<x>&")}})
	got, err := RowsToXML(rows)
	if err != nil {
		t.Fatalf("RowsToXML: %v", err)
	}
	for _, want := range []string{"<user_name>a</user_name>", "<_2fa>b</_2fa>", "&lt;x&gt;&amp;"} {
		if !strings.Contains(got, want) {
			t.Fatalf("XML %q does not contain %q", got, want)
		}
	}
}

func TestRowsToXMLPreservesDuplicateColumns(t *testing.T) {
	// Duplicate columns must keep both values in order, as the legacy
	// streaming implementation did. A map-based XML path would output
	// <a>2</a><a>2</a> here instead.
	rows := openRows(t,
		[]string{"a", "a"},
		[][]driver.Value{{[]byte("1"), []byte("2")}})
	got, err := RowsToXML(rows)
	if err != nil {
		t.Fatalf("RowsToXML: %v", err)
	}
	// Indentation puts whitespace between elements, so check both
	// elements in order rather than as one contiguous string.
	first := strings.Index(got, "<a>1</a>")
	second := strings.Index(got, "<a>2</a>")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("duplicate-column XML = %q, want <a>1</a> before <a>2</a>", got)
	}

	// The map APIs cannot represent duplicates, so they keep the last
	// value; that behaviour is documented, not a bug.
	rows = openRows(t,
		[]string{"a", "a"},
		[][]driver.Value{{[]byte("1"), []byte("2")}})
	m, err := RowsToMap(rows)
	if err != nil {
		t.Fatalf("RowsToMap: %v", err)
	}
	if m[0]["a"] != "2" {
		t.Fatalf("duplicate-column map = %#v, want last value \"2\"", m[0])
	}
}

func TestRowsToXMLKeepsNonASCIIColumnNames(t *testing.T) {
	rows := openRows(t,
		[]string{"姓名", "地址", "café"},
		[][]driver.Value{{[]byte("王"), []byte("台北"), []byte("x")}})
	got, err := RowsToXML(rows)
	if err != nil {
		t.Fatalf("RowsToXML: %v", err)
	}
	for _, want := range []string{"<姓名>王</姓名>", "<地址>台北</地址>", "<café>x</café>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("XML %q does not contain %q", got, want)
		}
	}
}

func TestRowsErrorIsPropagated(t *testing.T) {
	boom := errors.New("boom")
	rows := openRowsWithErr(t,
		[]string{"uid"},
		[][]driver.Value{{[]byte("1")}, {[]byte("2")}},
		1, boom)
	got, err := RowsToMap(rows)
	if err == nil {
		t.Fatalf("RowsToMap: got nil error, want %v (partial result %#v should be discarded)", boom, got)
	}
	if got != nil {
		t.Fatalf("RowsToMap on error should return nil, got %#v", got)
	}
}

func TestNilRowsAndEmptyResult(t *testing.T) {
	if _, err := RowsToMap(nil); err == nil {
		t.Fatal("RowsToMap(nil): want error")
	}
	if _, err := RowsToXML(nil); err == nil {
		t.Fatal("RowsToXML(nil): want error")
	}
	if _, err := RowsToJSON(nil); err == nil {
		t.Fatal("RowsToJSON(nil): want error")
	}
	if _, err := RowsToSlice(nil); err == nil {
		t.Fatal("RowsToSlice(nil): want error")
	}
	if _, err := RowsToAny(nil); err == nil {
		t.Fatal("RowsToAny(nil): want error")
	}

	rows := openRows(t, []string{"uid"}, nil)
	slice, err := RowsToSlice(rows)
	if err != nil {
		t.Fatalf("RowsToSlice empty: %v", err)
	}
	if slice == nil || len(slice) != 0 {
		t.Fatalf("empty slice = %#v, want non-nil empty slice", slice)
	}

	rows = openRows(t, []string{}, nil)
	if _, err := RowsToMap(rows); err == nil {
		t.Fatal("no-column query: want error, got nil")
	}
}

func TestSanitizeXMLName(t *testing.T) {
	cases := map[string]string{
		"":          "col",
		"uid":       "uid",
		"user name": "user_name",
		"2fa":       "_2fa",
		"a-b.c":     "a-b.c",
		"a/b":       "a_b",
		"姓名":        "姓名",
		"café":      "café",
		"a:b":       "a_b",
	}
	for in, want := range cases {
		if got := sanitizeXMLName(in); got != want {
			t.Errorf("sanitizeXMLName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRowsToAnyConvertsByteValuesByColumnType(t *testing.T) {
	// MySQL-style: numbers arrive as []byte. The column type, not the
	// driver value, must decide the Go type.
	rows := openRowsWithTypes(t,
		[]string{"id", "price", "active", "created", "name"},
		[]string{"INT", "DECIMAL", "BOOLEAN", "DATETIME", "VARCHAR"},
		[][]driver.Value{{[]byte("42"), []byte("9.5"), []byte("1"), []byte("2018-03-20 00:00:00"), []byte("x")}})
	got, err := RowsToAny(rows)
	if err != nil {
		t.Fatalf("RowsToAny: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	row := got[0]
	if row["id"] != int64(42) {
		t.Fatalf("id = %#v (%T), want int64 42", row["id"], row["id"])
	}
	if row["price"] != float64(9.5) {
		t.Fatalf("price = %#v (%T), want float64 9.5", row["price"], row["price"])
	}
	if row["active"] != true {
		t.Fatalf("active = %#v (%T), want bool true", row["active"], row["active"])
	}
	wantTime := time.Date(2018, 3, 20, 0, 0, 0, 0, time.UTC)
	if row["created"] != wantTime {
		t.Fatalf("created = %#v (%T), want %v", row["created"], row["created"], wantTime)
	}
	if row["name"] != "x" {
		t.Fatalf("name = %#v, want \"x\"", row["name"])
	}
}

func TestRowsToAnyPostgresTypeNames(t *testing.T) {
	rows := openRowsWithTypes(t,
		[]string{"id", "amount", "ok", "at"},
		[]string{"int4", "numeric", "bool", "timestamptz"},
		[][]driver.Value{{[]byte("7"), []byte("2.25"), []byte("0"), []byte("2018-03-20T00:00:00Z")}})
	got, err := RowsToAny(rows)
	if err != nil {
		t.Fatalf("RowsToAny: %v", err)
	}
	row := got[0]
	if row["id"] != int64(7) || row["amount"] != float64(2.25) || row["ok"] != false {
		t.Fatalf("row = %#v", row)
	}
	if row["at"] != time.Date(2018, 3, 20, 0, 0, 0, 0, time.UTC) {
		t.Fatalf("at = %#v", row["at"])
	}
}

func TestRowsToAnyUnparsableValueFallsBackToString(t *testing.T) {
	rows := openRowsWithTypes(t,
		[]string{"id"},
		[]string{"INT"},
		[][]driver.Value{{[]byte("not-a-number")}})
	got, err := RowsToAny(rows)
	if err != nil {
		t.Fatalf("RowsToAny: %v", err)
	}
	if got[0]["id"] != "not-a-number" {
		t.Fatalf("id = %#v, want fallback string \"not-a-number\"", got[0]["id"])
	}
}

func TestIterAnyStreamsAndEarlyBreak(t *testing.T) {
	rows := openRows(t,
		[]string{"n"},
		[][]driver.Value{{[]byte("a")}, {[]byte("b")}, {[]byte("c")}})
	var got []string
	for row, err := range IterAny(rows) {
		if err != nil {
			t.Fatalf("IterAny: %v", err)
		}
		got = append(got, row["n"].(string))
		if len(got) == 1 {
			break // early break must stop iteration without an error
		}
	}
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("got %#v, want [a]", got)
	}
}

func TestIterAnyPropagatesError(t *testing.T) {
	boom := errors.New("boom")
	rows := openRowsWithErr(t,
		[]string{"uid"},
		[][]driver.Value{{[]byte("1")}, {[]byte("2")}},
		1, boom)
	var count int
	var iterErr error
	for _, err := range IterAny(rows) {
		if err != nil {
			iterErr = err
			break
		}
		count++
	}
	if iterErr == nil {
		t.Fatal("IterAny: want error, got nil")
	}
	if count != 1 {
		t.Fatalf("count = %d, want 1 row before the error", count)
	}
}

func TestForEachAnyStopsOnCallbackError(t *testing.T) {
	rows := openRows(t,
		[]string{"n"},
		[][]driver.Value{{[]byte("a")}, {[]byte("b")}, {[]byte("c")}})
	stop := errors.New("stop")
	var seen int
	err := ForEachAny(rows, func(row map[string]any) error {
		seen++
		return stop
	})
	if err != stop {
		t.Fatalf("ForEachAny err = %v, want %v", err, stop)
	}
	if seen != 1 {
		t.Fatalf("seen = %d, want 1 (callback error must stop iteration)", seen)
	}
}

func TestForEachStringAndNilErrors(t *testing.T) {
	if err := ForEachAny(nil, func(map[string]any) error { return nil }); err == nil {
		t.Fatal("ForEachAny(nil): want error")
	}
	if err := ForEachString(nil, func(map[string]string) error { return nil }); err == nil {
		t.Fatal("ForEachString(nil): want error")
	}
	rows := openRows(t, []string{"n"}, [][]driver.Value{{[]byte("a")}, {[]byte("b")}})
	var got []string
	if err := ForEachString(rows, func(row map[string]string) error {
		got = append(got, row["n"])
		return nil
	}); err != nil {
		t.Fatalf("ForEachString: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("got %#v, want [a b]", got)
	}
}

func TestWriteJSONStreams(t *testing.T) {
	rows := openRows(t,
		[]string{"uid", "username"},
		[][]driver.Value{{int64(1), []byte("Trump")}, {int64(2), []byte("x")}})
	var buf strings.Builder
	if err := WriteJSON(&buf, rows); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal([]byte(buf.String()), &decoded); err != nil {
		t.Fatalf("WriteJSON output %q does not decode: %v", buf.String(), err)
	}
	if len(decoded) != 2 || decoded[0]["uid"] != float64(1) || decoded[1]["username"] != "x" {
		t.Fatalf("decoded = %#v", decoded)
	}

	empty := openRows(t, []string{"uid"}, nil)
	buf.Reset()
	if err := WriteJSON(&buf, empty); err != nil {
		t.Fatalf("WriteJSON empty: %v", err)
	}
	if buf.String() != "[]" {
		t.Fatalf("empty WriteJSON = %q, want \"[]\"", buf.String())
	}

	if err := WriteJSON(nil, openRows(t, []string{"uid"}, nil)); err == nil {
		t.Fatal("WriteJSON(nil writer): want error")
	}
	if err := WriteJSON(&buf, nil); err == nil {
		t.Fatal("WriteJSON(nil rows): want error")
	}
}

func TestWriteXMLStreams(t *testing.T) {
	rows := openRows(t,
		[]string{"uid", "username"},
		[][]driver.Value{{[]byte("1"), []byte("Trump")}, {[]byte("2"), []byte("x")}})
	var buf strings.Builder
	if err := WriteXML(&buf, rows, "Table", "Row"); err != nil {
		t.Fatalf("WriteXML: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`<?xml version="1.0" encoding="UTF-8"?>`, "<Table>", "<Row0>", "<uid>1</uid>", "<Row1>", "</Table>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("WriteXML output %q does not contain %q", out, want)
		}
	}

	// Duplicate columns must survive streaming XML, as in RowsToXML.
	dup := openRows(t, []string{"a", "a"}, [][]driver.Value{{[]byte("1"), []byte("2")}})
	buf.Reset()
	if err := WriteXML(&buf, dup, "", ""); err != nil {
		t.Fatalf("WriteXML duplicates: %v", err)
	}
	first := strings.Index(buf.String(), "<a>1</a>")
	second := strings.Index(buf.String(), "<a>2</a>")
	if first < 0 || second < 0 || first >= second {
		t.Fatalf("duplicate-column WriteXML = %q, want <a>1</a> before <a>2</a>", buf.String())
	}

	if err := WriteXML(nil, openRows(t, []string{"uid"}, nil), "", ""); err == nil {
		t.Fatal("WriteXML(nil writer): want error")
	}
	if err := WriteXML(&buf, nil, "", ""); err == nil {
		t.Fatal("WriteXML(nil rows): want error")
	}
}
