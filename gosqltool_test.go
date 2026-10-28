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

	if slice, err := RowsToSlice(nil); err == nil || slice != nil {
		t.Fatalf("RowsToSlice(nil) = %#v, %v; want nil slice and error", slice, err)
	}
	if any, err := RowsToAny(nil); err == nil || any != nil {
		t.Fatalf("RowsToAny(nil) = %#v, %v; want nil slice and error", any, err)
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
		"٣abc":      "_abc",
		"a３b":       "a_b",
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
	if row["id"] != int64(7) {
		t.Fatalf("id = %#v, want int64 7", row["id"])
	}
	if row["amount"] != float64(2.25) {
		t.Fatalf("amount = %#v, want float64 2.25", row["amount"])
	}
	if row["ok"] != false {
		t.Fatalf("ok = %#v, want bool false", row["ok"])
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
	if !errors.Is(iterErr, boom) {
		t.Fatalf("IterAny err = %v, want errors.Is boom", iterErr)
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
	want := []map[string]any{
		{"uid": float64(1), "username": "Trump"},
		{"uid": float64(2), "username": "x"},
	}
	if !reflect.DeepEqual(decoded, want) {
		t.Fatalf("decoded = %#v, want %#v", decoded, want)
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
	for _, want := range []string{`<?xml version="1.0" encoding="UTF-8"?>`, "<Table>", "<Row0>", "<uid>1</uid>", "<username>Trump</username>", "<Row1>", "<uid>2</uid>", "<username>x</username>", "</Table>"} {
		if !strings.Contains(out, want) {
			t.Fatalf("WriteXML output %q does not contain %q", out, want)
		}
	}

	// Duplicate columns must survive streaming XML, as in RowsToXML.
	// Empty names here also prove the RowData / Row_ defaults.
	dup := openRows(t, []string{"a", "a"}, [][]driver.Value{{[]byte("1"), []byte("2")}})
	buf.Reset()
	if err := WriteXML(&buf, dup, "", ""); err != nil {
		t.Fatalf("WriteXML duplicates: %v", err)
	}
	for _, want := range []string{"<RowData>", "<Row_0>"} {
		if !strings.Contains(buf.String(), want) {
			t.Fatalf("WriteXML defaults output %q does not contain %q", buf.String(), want)
		}
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

func TestRowsToAnyNormalizesDriverTypedValues(t *testing.T) {
	// Documented normalization: a value the driver already typed is
	// still normalized to the column kind, not passed through verbatim.
	rows := openRowsWithTypes(t,
		[]string{"i", "f", "b"},
		[]string{"INT", "DECIMAL", "BOOLEAN"},
		[][]driver.Value{{float64(42), int64(3), int64(1)}})
	got, err := RowsToAny(rows)
	if err != nil {
		t.Fatalf("RowsToAny: %v", err)
	}
	row := got[0]
	if row["i"] != int64(42) {
		t.Fatalf("i = %#v (%T), want int64 42", row["i"], row["i"])
	}
	if row["f"] != float64(3) {
		t.Fatalf("f = %#v (%T), want float64 3", row["f"], row["f"])
	}
	if row["b"] != true {
		t.Fatalf("b = %#v (%T), want bool true", row["b"], row["b"])
	}
}

func TestRowsToAnyUnsignedTypes(t *testing.T) {
	rows := openRowsWithTypes(t,
		[]string{"a", "b", "c"},
		[]string{"UNSIGNED INT", "UNSIGNED BIGINT", "UNSIGNED BIG INT"},
		[][]driver.Value{{[]byte("7"), []byte("42"), []byte("9")}})
	got, err := RowsToAny(rows)
	if err != nil {
		t.Fatalf("RowsToAny: %v", err)
	}
	row := got[0]
	if row["a"] != int64(7) || row["b"] != int64(42) || row["c"] != int64(9) {
		t.Fatalf("row = %#v, want int64 7/42/9", row)
	}

	// INT UNSIGNED is the suffix spelling of the same modifier.
	rows = openRowsWithTypes(t,
		[]string{"a"},
		[]string{"INT UNSIGNED"},
		[][]driver.Value{{[]byte("11")}})
	got, err = RowsToAny(rows)
	if err != nil {
		t.Fatalf("RowsToAny suffix: %v", err)
	}
	if got[0]["a"] != int64(11) {
		t.Fatalf("a = %#v, want int64 11", got[0]["a"])
	}

	// Above the int64 range, ParseInt fails and the value falls back
	// to its string form, preserving the value instead of erroring.
	rows = openRowsWithTypes(t,
		[]string{"huge"},
		[]string{"UNSIGNED BIGINT"},
		[][]driver.Value{{[]byte("18446744073709551615")}})
	got, err = RowsToAny(rows)
	if err != nil {
		t.Fatalf("RowsToAny overflow: %v", err)
	}
	if got[0]["huge"] != "18446744073709551615" {
		t.Fatalf("huge = %#v, want fallback string", got[0]["huge"])
	}
}

func TestRowsToAnyNaNAndInfFallbackToString(t *testing.T) {
	rows := openRowsWithTypes(t,
		[]string{"x", "y", "z"},
		[]string{"DECIMAL", "DECIMAL", "DECIMAL"},
		[][]driver.Value{{[]byte("NaN"), []byte("Inf"), []byte("-Inf")}})
	got, err := RowsToAny(rows)
	if err != nil {
		t.Fatalf("RowsToAny: %v", err)
	}
	if got[0]["x"] != "NaN" || got[0]["y"] != "Inf" || got[0]["z"] != "-Inf" {
		t.Fatalf("row = %#v, want NaN/Inf/-Inf strings", got[0])
	}

	// The same values must not make RowsToJSON fail the whole call.
	rows = openRowsWithTypes(t,
		[]string{"x"},
		[]string{"DECIMAL"},
		[][]driver.Value{{[]byte("NaN")}})
	out, err := RowsToJSON(rows)
	if err != nil {
		t.Fatalf("RowsToJSON NaN: %v", err)
	}
	if out != `[{"x":"NaN"}]` {
		t.Fatalf("RowsToJSON = %q, want [{\"x\":\"NaN\"}]", out)
	}
}

func TestRowsToAnyTimeWithoutTimezoneAndUnknownType(t *testing.T) {
	rows := openRowsWithTypes(t,
		[]string{"t", "u", "v", "w"},
		[]string{"TIME", "", "FOO", "TIMESTAMP"},
		[][]driver.Value{{[]byte("12:34:56"), []byte("42"), []byte("42"), []byte("2018-03-20T00:00:00+0000")}})
	got, err := RowsToAny(rows)
	if err != nil {
		t.Fatalf("RowsToAny: %v", err)
	}
	row := got[0]
	if row["t"] != "12:34:56" {
		t.Fatalf("TIME t = %#v, want string \"12:34:56\"", row["t"])
	}
	if row["u"] != "42" {
		t.Fatalf("unknown-type u = %#v, want string \"42\"", row["u"])
	}
	if row["v"] != "42" {
		t.Fatalf("unknown-type v = %#v, want string \"42\"", row["v"])
	}
	// time.Parse with a numeric +0000 offset yields a fixed zone with
	// an empty name, not time.UTC, so compare instants, not ==.
	wt, ok := row["w"].(time.Time)
	if !ok || !wt.Equal(time.Date(2018, 3, 20, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("w = %#v, want instant 2018-03-20 UTC (no-colon offset layout)", row["w"])
	}
}

func TestIterString(t *testing.T) {
	rows := openRows(t,
		[]string{"n"},
		[][]driver.Value{{[]byte("a")}, {[]byte("b")}, {[]byte("c")}})
	var got []string
	for row, err := range IterString(rows) {
		if err != nil {
			t.Fatalf("IterString: %v", err)
		}
		got = append(got, row["n"])
		if len(got) == 1 {
			break
		}
	}
	if len(got) != 1 || got[0] != "a" {
		t.Fatalf("early break got %#v, want [a]", got)
	}

	// Nil rows yield one error, empty result yields zero rows and no error.
	var iterErr error
	for _, err := range IterString(nil) {
		iterErr = err
	}
	if iterErr == nil {
		t.Fatal("IterString(nil): want error")
	}
	empty := openRows(t, []string{"n"}, nil)
	var count int
	for _, err := range IterString(empty) {
		if err != nil {
			t.Fatalf("IterString empty: %v", err)
		}
		count++
	}
	if count != 0 {
		t.Fatalf("IterString empty count = %d, want 0", count)
	}

	// Mid-stream error: one row, then the error, with errors.Is intact.
	boom := errors.New("boom")
	errRows := openRowsWithErr(t,
		[]string{"uid"},
		[][]driver.Value{{[]byte("1")}, {[]byte("2")}},
		1, boom)
	count = 0
	iterErr = nil
	for _, err := range IterString(errRows) {
		if err != nil {
			iterErr = err
			break
		}
		count++
	}
	if !errors.Is(iterErr, boom) {
		t.Fatalf("IterString err = %v, want errors.Is boom", iterErr)
	}
	if count != 1 {
		t.Fatalf("IterString count = %d, want 1 row before the error", count)
	}
}

func TestStreamingWritersAndForEachMidStreamError(t *testing.T) {
	boom := errors.New("boom")
	mk := func() *sql.Rows {
		return openRowsWithErr(t,
			[]string{"uid"},
			[][]driver.Value{{[]byte("1")}, {[]byte("2")}},
			1, boom)
	}

	var buf strings.Builder
	if err := WriteJSON(&buf, mk()); !errors.Is(err, boom) {
		t.Fatalf("WriteJSON mid-stream err = %v, want errors.Is boom", err)
	}
	if !strings.HasPrefix(buf.String(), "[") || strings.HasSuffix(buf.String(), "]") {
		t.Fatalf("WriteJSON partial output = %q, want a partial array without closing ]", buf.String())
	}

	buf.Reset()
	if err := WriteXML(&buf, mk(), "Table", "Row"); !errors.Is(err, boom) {
		t.Fatalf("WriteXML mid-stream err = %v, want errors.Is boom", err)
	}
	if !strings.Contains(buf.String(), "<Row0>") || strings.Contains(buf.String(), "</Table>") {
		t.Fatalf("WriteXML partial output = %q, want first row but no closing table", buf.String())
	}

	if err := ForEachAny(mk(), func(map[string]any) error { return nil }); !errors.Is(err, boom) {
		t.Fatalf("ForEachAny mid-stream err = %v, want errors.Is boom", err)
	}
	if err := ForEachString(mk(), func(map[string]string) error { return nil }); !errors.Is(err, boom) {
		t.Fatalf("ForEachString mid-stream err = %v, want errors.Is boom", err)
	}
}

func TestStreamingWritersNoColumnFailsWithNoOutput(t *testing.T) {
	var buf strings.Builder
	rows := openRows(t, []string{}, nil)
	if err := WriteJSON(&buf, rows); err == nil {
		t.Fatal("WriteJSON no-column: want error")
	}
	if buf.String() != "" {
		t.Fatalf("WriteJSON no-column output = %q, want empty (pre-validation)", buf.String())
	}

	buf.Reset()
	rows = openRows(t, []string{}, nil)
	if err := WriteXML(&buf, rows, "Table", "Row"); err == nil {
		t.Fatal("WriteXML no-column: want error")
	}
	if buf.String() != "" {
		t.Fatalf("WriteXML no-column output = %q, want empty (pre-validation)", buf.String())
	}

	// Empty results on the iterators and ForEach: zero rows, no error.
	empty := openRows(t, []string{"n"}, nil)
	var count int
	for _, err := range IterAny(empty) {
		if err != nil {
			t.Fatalf("IterAny empty: %v", err)
		}
		count++
	}
	if count != 0 {
		t.Fatalf("IterAny empty count = %d, want 0", count)
	}
	empty = openRows(t, []string{"n"}, nil)
	if err := ForEachAny(empty, func(map[string]any) error { return nil }); err != nil {
		t.Fatalf("ForEachAny empty: %v", err)
	}
}

func TestWriteXMLDefaultNames(t *testing.T) {
	rows := openRows(t, []string{"uid"}, [][]driver.Value{{[]byte("1")}})
	var buf strings.Builder
	if err := WriteXML(&buf, rows, "", ""); err != nil {
		t.Fatalf("WriteXML: %v", err)
	}
	if !strings.Contains(buf.String(), "<RowData>") || !strings.Contains(buf.String(), "<Row_0>") {
		t.Fatalf("WriteXML defaults = %q, want <RowData> and <Row_0>", buf.String())
	}
}

func TestForEachNilFunc(t *testing.T) {
	rows := openRows(t, []string{"n"}, [][]driver.Value{{[]byte("a")}})
	if err := ForEachAny(rows, nil); err == nil {
		t.Fatal("ForEachAny(nil fn): want error, got nil (must not panic)")
	}
	rows = openRows(t, []string{"n"}, [][]driver.Value{{[]byte("a")}})
	if err := ForEachString(rows, nil); err == nil {
		t.Fatal("ForEachString(nil fn): want error, got nil (must not panic)")
	}
}
