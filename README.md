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
| `RowsToAny(rows)` | `[]map[string]any` | Keeps driver types where possible (int64, float64, bool, time.Time, …). NULL is `nil`, not `""`. Which type a column gets depends on the driver: a MySQL driver returning `[]byte` for numbers still yields strings. |
| `RowsToJSON(rows)` | JSON array string | Built on `RowsToAny`: numbers stay numbers, NULL is `null`, times are RFC3339Nano. Empty result is `"[]"`. A value JSON cannot encode (NaN, infinite float) makes the call fail instead of producing invalid JSON. |
| `RowsToXML(rows)` | XML string | Original API. Table/row names come from the `TableName` / `RowName` variables. |
| `RowsToXMLWithNames(rows, table, row)` | XML string | Same as `RowsToXML`, but the names are parameters instead of globals. Empty names fall back to the defaults. |

On any read error, the functions return `nil` / `""` plus the error
(including errors reported by `rows.Err()` after iteration) instead of
a partial result. Passing `nil` rows returns an error.

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
