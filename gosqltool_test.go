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
	rows     [][]driver.Value
	idx      int
	errAfter int // index of the Next call that returns an error, -1 for none
	nextErr  error
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
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
	return &fakeRows{cols: c.cols, rows: c.rows, errAfter: c.errAfter, nextErr: c.nextErr}, nil
}

type fakeDriver struct{ conn *fakeConn }

func (d *fakeDriver) Open(name string) (driver.Conn, error) { return d.conn, nil }

// openRows builds a *sql.Rows from a fake driver, so tests need no
// real database.
func openRows(t *testing.T, cols []string, rows [][]driver.Value) *sql.Rows {
	t.Helper()
	return openRowsWithErr(t, cols, rows, -1, nil)
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
