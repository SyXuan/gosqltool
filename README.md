# gosqltool

Gosqltool helps you simply access SQL result values when you don't know
the keys in advance. You don't need to declare a struct and scan into it.

## Install

```bash
go get github.com/SyXuan/gosqltool
```

Requires Go 1.23 or later.

## Functions

| Function | Returns | Notes |
| --- | --- | --- |
| `RowsToMap(rows)` | `map[int]map[string]string` | Original API, keys are 0-based row indexes. NULL becomes `""`. |
| `RowsToSlice(rows)` | `[]map[string]string` | Same string values as `RowsToMap`, but in row order. Prefer this for new code. |
| `RowsToAny(rows)` | `[]map[string]any` | Keeps driver types where possible (int64, float64, bool, time.Time, …). NULL is `nil`, not `""`. A value that arrives as `[]byte` or string is converted using the column's database type (see Typed conversion below), so a MySQL INT column no longer yields a string. |
| `RowsToJSON(rows)` | JSON array string | Built on `RowsToAny`: numbers stay numbers, NULL is `null`, times are RFC3339Nano. Empty result is `"[]"`. A value JSON cannot encode (NaN, infinite float) makes the call fail instead of producing invalid JSON. |
| `RowsToXML(rows)` | XML string | Original API. Table/row names come from the `TableName` / `RowName` variables. |
| `RowsToXMLWithNames(rows, table, row)` | XML string | Same as `RowsToXML`, but the names are parameters instead of globals. Empty names fall back to the defaults. |
| `IterAny(rows)` | `iter.Seq2[map[string]any, error]` | Streams one typed row at a time, same values as `RowsToAny`, without loading the whole result. |
| `IterString(rows)` | `iter.Seq2[map[string]string, error]` | Streams one string row at a time, same values as `RowsToSlice`. |
| `ForEachAny(rows, fn)` / `ForEachString(rows, fn)` | `error` | Calls `fn` once per row and stops at the first error from `fn` or from iteration. |
| `WriteJSON(w, rows)` | `error` | Streams rows to `w` as a JSON array, one row at a time. Empty result writes `"[]"`. |
| `WriteXML(w, rows, table, row)` | `error` | Streams rows to `w` as XML, one row at a time, preserving duplicate column names and order. Empty names fall back to the defaults. |

On any read error, the functions return `nil` / `""` plus the error
(including errors reported by `rows.Err()` after iteration) instead of
a partial result. Passing `nil` rows returns an error.

## Streaming

The collecting APIs above load the whole result into memory. For a
large result, iterate or write one row at a time instead:

```go
for row, err := range gosqltool.IterAny(rows) {
    if err != nil {
        checkErr(err)
    }
    fmt.Println(row["username"])
}
```

Or use the callback form, or stream straight to a writer (for
example an HTTP response):

```go
err := gosqltool.WriteJSON(w, rows)
checkErr(err)
```

Streaming APIs do not close `rows`: the caller still owns it and
must close it, exactly as with the collecting APIs. Breaking out of
an iterator early simply stops iteration, with no error. Unlike the
collecting APIs, `WriteJSON` / `WriteXML` cannot take back output
already written, so a mid-stream read or encode error leaves a
partial array or document in the writer; the error is still returned.

## Typed conversion

`RowsToAny`, `IterAny`, `ForEachAny`, `RowsToJSON` and `WriteJSON`
convert a `[]byte` or string value using the column's database type
(`rows.ColumnTypes()`), covering common MySQL, PostgreSQL and SQLite
spellings:

- INT-like columns yield `int64`
- FLOAT / DOUBLE / REAL / DECIMAL / NUMERIC yield `float64`
- BOOL / BOOLEAN yield `bool`
- DATE / DATETIME / TIMESTAMP yield `time.Time`

Values the driver already typed pass through unchanged. A value that
cannot be parsed for its column type falls back to its string form
instead of failing the row. DECIMAL and NUMERIC become `float64`, so
very large or high-precision decimals may lose precision. TIME
columns stay strings, because a time of day has no date to put into
a `time.Time`. Columns whose type the driver does not report keep
the previous behaviour (`[]byte` becomes a string).

## Example

### RowsToMap

```go
rows, err := db.Query("SELECT * FROM userinfo")
defer rows.Close()
checkErr(err)

d, err := gosqltool.RowsToMap(rows)
checkErr(err)
fmt.Printf("%#v", d)
```

The output will be

```go
map[int]map[string]string{
    0: {"uid": "11", "username": "Trump_new", "departname": "Company", "created": "2018-03-20T00:00:00Z"},
    1: {"uid": "13", "username": "Trump_new", "departname": "Company", "created": "2018-03-20T00:00:00Z"},
}
```

And you can easily get a value via map, such as

```go
d[0]["username"]
```

### RowsToJSON

```go
d, err := gosqltool.RowsToJSON(rows)
checkErr(err)
fmt.Println(d)
```

```json
[{"created":"2018-03-20T00:00:00Z","uid":1,"username":"Trump"}]
```

### RowsToXML

XML is a very simple way to send data for a web API or for Windows
executable DLL files.

```go
d, err := gosqltool.RowsToXML(rows)
checkErr(err)
fmt.Println(d)
```

The output will be

```xml
<?xml version="1.0" encoding="UTF-8"?>
<RowData>
  <Row_0>
    <uid>1</uid>
    <username>Trump</username>
    <departname>Company</departname>
    <created>2018-03-20T00:00:00Z</created>
  </Row_0>
  <Row_1>
    <uid>2</uid>
    <username>Trump_new</username>
    <departname>Company</departname>
    <created>2018-03-20T00:00:00Z</created>
  </Row_1>
</RowData>
```

Or you can customize the table and row names without touching globals:

```go
d, err := gosqltool.RowsToXMLWithNames(rows, "Table", "Row")
checkErr(err)
fmt.Println(d)
```

```xml
<?xml version="1.0" encoding="UTF-8"?>
<Table>
  <Row0>
    <uid>1</uid>
    <username>Trump</username>
    <departname>Company</departname>
    <created>2018-03-20T00:00:00Z</created>
  </Row0>
  <Row1>
    <uid>2</uid>
    <username>Trump_new</username>
    <departname>Company</departname>
    <created>2018-03-20T00:00:00Z</created>
  </Row1>
</Table>
```

Table, row and column names are sanitized into valid XML element
names (for example `"user name"` becomes `<user_name>`, and `"2fa"`
becomes `<_2fa>`), and values are XML-escaped. Valid non-ASCII names
are kept as-is, so a Chinese column name stays readable in the output.
The XML path preserves duplicate column names and their order, as the
original streaming implementation did; the map APIs cannot represent
duplicates, so there the last value wins. The old `TableName` /
`RowName` globals still work, but they are not safe for concurrent use;
set them once at startup if you use them, or prefer
`RowsToXMLWithNames`.

## Migration notes

- **Typed conversion (new in this change):** `RowsToAny` and
  `RowsToJSON` used to keep a `[]byte` value as a string whenever
  the driver returned it that way (for example a MySQL INT column
  yielded `"42"`). They now convert it using the column's database
  type, so the same column yields `int64(42)`. This is the intended
  fix, but it changes output for drivers that return numbers, bools
  or times as `[]byte` or strings. Columns with no reported type,
  unparsable values (which fall back to strings), and values the
  driver already typed are unchanged.
- The module path is now `github.com/SyXuan/gosqltool` (capital `S`/`X`,
  matching the repository). The old README showed a lowercase path.
- `RowsToMap` and `RowsToXML` keep their original signatures and, for
  normal data, their original output, including duplicate columns in
  XML. Edge cases changed on purpose: `nil` rows used to panic in both
  functions and now return an error; a query with no columns used to
  panic in `RowsToXML` (indexing the first column) and silently return
  an empty result in `RowsToMap`, and now returns an error in both;
  a mid-iteration error now returns no partial result.
- NULL handling is unchanged for the string APIs (`""`). If you need to
  tell NULL apart from an empty string, use `RowsToAny` / `RowsToJSON`.

## Development

```bash
gofmt -l .
go vet ./...
go test ./...
```

Each example in this README is an independent query: a `*sql.Rows`
value is consumed by the first call, so do not reuse the same `rows`
across examples.

Tests use a fake `database/sql` driver in `gosqltool_test.go`, so no
real database is needed.

## Contributing

This project accepts contributions. Just fork the repo and submit a pull request!
