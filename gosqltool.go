package gosqltool

import (
	"database/sql"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"iter"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/beevik/etree"
)

// XML default table and row names.
//
// Note: these globals are not safe for concurrent use; set them once at
// startup if you use them at all. For per-call names, use
// RowsToXMLWithNames instead.
var (
	TableName = "RowData"
	RowName   = "Row_"
)

const (
	defaultTableName = "RowData"
	defaultRowName   = "Row_"
)

// stringCell is one column value in its original position. It exists so
// the XML path can preserve duplicate column names and their order,
// which a map cannot: map APIs collapse duplicates, last value wins.
type stringCell struct {
	name  string
	value string
}

// valueKind is the column type category used to convert a driver value
// that arrived as []byte or string into a typed Go value. kindUnknown
// means the driver gave no usable database type name, and the value is
// left as the driver returned it ([]byte becomes string, as before).
type valueKind int

const (
	kindUnknown valueKind = iota
	kindInt
	kindFloat
	kindBool
	kindTime
)

// columnInfo pairs a column name with its converted-value kind.
type columnInfo struct {
	name string
	kind valueKind
}

// normalizeTypeName upper-cases a database type name and strips any
// size suffix, so "varchar(255)" and "VARCHAR" map the same way.
func normalizeTypeName(name string) string {
	name = strings.ToUpper(strings.TrimSpace(name))
	if i := strings.IndexByte(name, '('); i >= 0 {
		name = strings.TrimSpace(name[:i])
	}
	return name
}

// kindFromTypeName maps a database type name to a value kind. It covers
// the common MySQL, PostgreSQL and SQLite spellings; anything else is
// kindUnknown, which preserves the previous behaviour. TIME columns
// are deliberately not kindTime: a time of day has no date, so keeping
// it as a string avoids inventing a "0000-01-01" date in JSON output.
func kindFromTypeName(name string) valueKind {
	switch normalizeTypeName(name) {
	case "INT", "INTEGER", "TINYINT", "SMALLINT", "MEDIUMINT", "BIGINT",
		"INT1", "INT2", "INT4", "INT8", "SERIAL", "SMALLSERIAL", "BIGSERIAL", "YEAR":
		return kindInt
	case "FLOAT", "DOUBLE", "REAL", "DECIMAL", "NUMERIC", "MONEY",
		"FLOAT4", "FLOAT8", "DEC", "FIXED":
		return kindFloat
	case "BOOL", "BOOLEAN":
		return kindBool
	case "DATE", "DATETIME", "TIMESTAMP", "TIMESTAMPTZ":
		return kindTime
	}
	// PostgreSQL spells some types with spaces, and "DOUBLE PRECISION"
	// is a float. TIMESTAMP WITH/WITHOUT TIME ZONE starts with TIMESTAMP.
	norm := normalizeTypeName(name)
	if strings.HasPrefix(norm, "TIMESTAMP") {
		return kindTime
	}
	if strings.HasPrefix(norm, "DOUBLE") {
		return kindFloat
	}
	return kindUnknown
}

// columnInfos returns one columnInfo per column. It returns an error
// for nil rows, for a Columns() error, for a ColumnTypes() error, and
// for a query with no columns, matching the error behaviour of the
// collecting APIs.
func columnInfos(rows *sql.Rows) ([]columnInfo, error) {
	if rows == nil {
		return nil, fmt.Errorf("gosqltool: nil rows")
	}
	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("gosqltool: columns: %w", err)
	}
	if len(cols) == 0 {
		return nil, fmt.Errorf("gosqltool: query returned no columns")
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return nil, fmt.Errorf("gosqltool: column types: %w", err)
	}
	infos := make([]columnInfo, len(cols))
	for i, name := range cols {
		kind := kindUnknown
		if i < len(types) && types[i] != nil {
			kind = kindFromTypeName(types[i].DatabaseTypeName())
		}
		infos[i] = columnInfo{name: name, kind: kind}
	}
	return infos, nil
}

// asString returns the string form of a driver value that arrived as
// []byte or string, and whether it did.
func asString(v any) (string, bool) {
	switch t := v.(type) {
	case []byte:
		return string(t), true
	case string:
		return t, true
	}
	return "", false
}

var timeLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// convertTypedValue converts one driver value using its column kind.
// Values the driver already typed (int64, float64, bool, time.Time)
// pass through unchanged. A []byte or string value that cannot be
// parsed for its kind falls back to its string form instead of
// failing the whole row, so a stray non-numeric value in an INT
// column does not lose the rest of the result. NULL (nil) stays nil.
func convertTypedValue(v any, kind valueKind) any {
	if v == nil {
		return nil
	}
	switch kind {
	case kindInt:
		switch t := v.(type) {
		case int64:
			return t
		case int:
			return int64(t)
		case float64:
			if t == float64(int64(t)) {
				return int64(t)
			}
			return t
		}
		if s, ok := asString(v); ok {
			if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				return n
			}
			return s
		}
		return v
	case kindFloat:
		switch t := v.(type) {
		case float64:
			return t
		case int64:
			return float64(t)
		case int:
			return float64(t)
		}
		if s, ok := asString(v); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
				return f
			}
			return s
		}
		return v
	case kindBool:
		switch t := v.(type) {
		case bool:
			return t
		case int64:
			return t != 0
		case int:
			return t != 0
		case float64:
			return t != 0
		}
		if s, ok := asString(v); ok {
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "1", "t", "true", "y", "yes", "on":
				return true
			case "0", "f", "false", "n", "no", "off":
				return false
			}
			return s
		}
		return v
	case kindTime:
		if t, ok := v.(time.Time); ok {
			return t
		}
		if s, ok := asString(v); ok {
			s = strings.TrimSpace(s)
			for _, layout := range timeLayouts {
				if t, err := time.Parse(layout, s); err == nil {
					return t
				}
			}
			return s
		}
		return v
	default:
		if b, ok := v.([]byte); ok {
			return string(b)
		}
		return v
	}
}

// iterCells yields one []stringCell per row, in query order, without
// accumulating the whole result. NULL becomes an empty string,
// matching the legacy RowsToMap behaviour. The iterator does not
// close rows: the caller owns rows and must close them, exactly as
// with the collecting APIs. Breaking out of the range early simply
// stops iteration; no error is yielded for the break itself. A nil
// rows value, a no-column query, a scan error, or a rows.Err() error
// is yielded once as (nil, err) and iteration stops.
func iterCells(rows *sql.Rows) iter.Seq2[[]stringCell, error] {
	return func(yield func([]stringCell, error) bool) {
		infos, err := columnInfos(rows)
		if err != nil {
			yield(nil, err)
			return
		}
		cols := make([]string, len(infos))
		for i, info := range infos {
			cols[i] = info.name
		}
		rawResult := make([][]byte, len(cols))
		dest := make([]any, len(cols))
		for i := range rawResult {
			dest[i] = &rawResult[i]
		}
		for rows.Next() {
			if err := rows.Scan(dest...); err != nil {
				yield(nil, fmt.Errorf("gosqltool: scan: %w", err))
				return
			}
			cells := make([]stringCell, len(cols))
			for i, raw := range rawResult {
				// A nil raw value means NULL; the legacy behaviour is an empty string.
				// string(raw) copies, so the next Scan cannot overwrite this row.
				cells[i] = stringCell{name: cols[i], value: string(raw)}
			}
			if !yield(cells, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(nil, fmt.Errorf("gosqltool: rows: %w", err))
		}
	}
}

// scanStringCells reads rows into ordered per-row cells. NULL becomes an
// empty string, matching the legacy RowsToMap behaviour; use RowsToAny
// when NULL must stay distinguishable.
func scanStringCells(rows *sql.Rows) ([]string, [][]stringCell, error) {
	infos, err := columnInfos(rows)
	if err != nil {
		return nil, nil, err
	}
	cols := make([]string, len(infos))
	for i, info := range infos {
		cols[i] = info.name
	}
	out := make([][]stringCell, 0)
	for cells, err := range iterCells(rows) {
		if err != nil {
			return nil, nil, err
		}
		out = append(out, cells)
	}
	return cols, out, nil
}

// stringRows reads rows into an ordered slice of map[colName]value.
// Duplicate column names collapse in a map, last value wins, exactly as
// in the legacy implementation; use the XML path if duplicates must be
// preserved in order.
func stringRows(rows *sql.Rows) ([]string, []map[string]string, error) {
	cols, cells, err := scanStringCells(rows)
	if err != nil {
		return nil, nil, err
	}
	out := make([]map[string]string, 0, len(cells))
	for _, rowCells := range cells {
		rowMap := make(map[string]string, len(rowCells))
		for _, cell := range rowCells {
			rowMap[cell.name] = cell.value
		}
		out = append(out, rowMap)
	}
	return cols, out, nil
}

// IterString yields one map[string]string per row, in query order,
// without accumulating the whole result. NULL becomes an empty
// string, duplicate column names collapse with last value wins, and
// the iterator does not close rows; see iterCells for the exact
// ownership and error behaviour.
func IterString(rows *sql.Rows) iter.Seq2[map[string]string, error] {
	return func(yield func(map[string]string, error) bool) {
		for cells, err := range iterCells(rows) {
			if err != nil {
				yield(nil, err)
				return
			}
			rowMap := make(map[string]string, len(cells))
			for _, cell := range cells {
				rowMap[cell.name] = cell.value
			}
			if !yield(rowMap, nil) {
				return
			}
		}
	}
}

// IterAny yields one map[string]any per row, in query order, without
// accumulating the whole result. Values follow the RowsToAny rules:
// driver types are kept where possible, and []byte or string values
// are converted using the column's database type (see
// kindFromTypeName), so a MySQL INT column that arrives as []byte
// yields int64 instead of a string. NULL becomes nil. The iterator
// does not close rows; see iterCells for the exact ownership and
// error behaviour.
func IterAny(rows *sql.Rows) iter.Seq2[map[string]any, error] {
	return func(yield func(map[string]any, error) bool) {
		infos, err := columnInfos(rows)
		if err != nil {
			yield(nil, err)
			return
		}
		vals := make([]any, len(infos))
		dest := make([]any, len(infos))
		for i := range vals {
			dest[i] = &vals[i]
		}
		for rows.Next() {
			if err := rows.Scan(dest...); err != nil {
				yield(nil, fmt.Errorf("gosqltool: scan: %w", err))
				return
			}
			rowMap := make(map[string]any, len(infos))
			for i, info := range infos {
				rowMap[info.name] = convertTypedValue(vals[i], info.kind)
			}
			if !yield(rowMap, nil) {
				return
			}
		}
		if err := rows.Err(); err != nil {
			yield(nil, fmt.Errorf("gosqltool: rows: %w", err))
		}
	}
}

// ForEachAny calls fn once per row, in query order, and stops at the
// first error from fn or from iteration. It does not close rows.
func ForEachAny(rows *sql.Rows, fn func(map[string]any) error) error {
	for row, err := range IterAny(rows) {
		if err != nil {
			return err
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	return nil
}

// ForEachString calls fn once per row, in query order, and stops at
// the first error from fn or from iteration. It does not close rows.
func ForEachString(rows *sql.Rows, fn func(map[string]string) error) error {
	for row, err := range IterString(rows) {
		if err != nil {
			return err
		}
		if err := fn(row); err != nil {
			return err
		}
	}
	return nil
}

// anyRows reads rows into an ordered slice of map[colName]value.
// Values follow the IterAny rules: driver types are kept where
// possible, []byte or string values are converted using the column's
// database type, and NULL becomes nil.
func anyRows(rows *sql.Rows) ([]map[string]any, error) {
	out := make([]map[string]any, 0)
	for row, err := range IterAny(rows) {
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, nil
}

// RowsToMap transfer sql res to map
// dataMap[rowCnt]map[colName]value, with rowCnt numbered from 0.
// On a read error it returns nil and the error, not a partial result.
// NULL becomes an empty string. Duplicate column names collapse in the
// map, last value wins; non-string driver values are converted through
// database/sql's []byte conversion, so for exact numeric values use
// RowsToAny instead.
func RowsToMap(rows *sql.Rows) (dataRes map[int]map[string]string, err error) {
	_, slice, err := stringRows(rows)
	if err != nil {
		return nil, err
	}
	dataMap := make(map[int]map[string]string, len(slice))
	for i, rowMap := range slice {
		dataMap[i] = rowMap
	}
	return dataMap, nil
}

// RowsToSlice is the ordered version of RowsToMap: it returns
// []map[string]string, so ranging over it keeps the original row order
// without sorting the int keys of a map. With no rows it returns an
// empty, non-nil slice. NULL becomes an empty string.
func RowsToSlice(rows *sql.Rows) ([]map[string]string, error) {
	_, slice, err := stringRows(rows)
	if err != nil {
		return nil, err
	}
	return slice, nil
}

// RowsToAny returns typed row data: numbers, booleans and times keep
// their types, and NULL becomes nil instead of an empty string, which
// makes it suitable for JSON output or type checks. With no rows it
// returns an empty, non-nil slice. Where the driver returns a value
// as []byte or string, the column's database type decides the Go
// type: INT-like columns yield int64, FLOAT/DECIMAL-like columns
// yield float64, BOOL columns yield bool, and DATE/DATETIME/
// TIMESTAMP columns yield time.Time. A value that cannot be parsed
// for its column type falls back to its string form. DECIMAL and
// NUMERIC become float64, so very large or high-precision decimals
// may lose precision; TIME columns stay strings.
func RowsToAny(rows *sql.Rows) ([]map[string]any, error) {
	return anyRows(rows)
}

// RowsToJSON converts rows to a JSON array string, for example:
//
//	[{"created":"2018-03-20T00:00:00Z","uid":1,"username":"Trump"}]
//
// Values follow the RowsToAny rules: numbers stay numbers, NULL is null,
// and times are RFC3339Nano. With no rows it returns "[]". A value that
// JSON cannot encode (for example a NaN or infinite float) makes the
// whole call return "" and an error instead of invalid JSON.
func RowsToJSON(rows *sql.Rows) (string, error) {
	slice, err := anyRows(rows)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(slice)
	if err != nil {
		return "", fmt.Errorf("gosqltool: json: %w", err)
	}
	return string(b), nil
}

// WriteJSON streams rows to w as a JSON array, one row at a time,
// without accumulating the whole result. Values follow the RowsToAny
// rules, and an empty result writes "[]". Unlike RowsToJSON, a
// mid-stream read or encode error cannot take back output already
// written, so the writer may be left with a partial array; the error
// is still returned. It does not close rows.
func WriteJSON(w io.Writer, rows *sql.Rows) error {
	if w == nil {
		return fmt.Errorf("gosqltool: nil writer")
	}
	if rows == nil {
		return fmt.Errorf("gosqltool: nil rows")
	}
	if _, err := w.Write([]byte("[")); err != nil {
		return fmt.Errorf("gosqltool: write: %w", err)
	}
	first := true
	for row, err := range IterAny(rows) {
		if err != nil {
			return err
		}
		if !first {
			if _, err := w.Write([]byte(",")); err != nil {
				return fmt.Errorf("gosqltool: write: %w", err)
			}
		}
		first = false
		b, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("gosqltool: json: %w", err)
		}
		if _, err := w.Write(b); err != nil {
			return fmt.Errorf("gosqltool: write: %w", err)
		}
	}
	if _, err := w.Write([]byte("]")); err != nil {
		return fmt.Errorf("gosqltool: write: %w", err)
	}
	return nil
}

// sanitizeXMLName converts a column name into a valid XML element name.
// XML names may start with a letter or "_" and may contain letters,
// digits, "_", "-" and "."; letters include non-ASCII letters, so a
// Chinese column name such as "姓名" is kept as-is. Characters outside
// that set (spaces, "/", ":", "<" and so on) become underscores, a
// leading digit, "-" or "." gets a "_" prefix, and an empty name
// becomes "col". Note that two different names can still sanitize to
// the same element name (for example "user name" and "user_name"); the
// XML path preserves both in order, but a map keyed by element name
// would collapse them.
func sanitizeXMLName(name string) string {
	if name == "" {
		return "col"
	}
	var b strings.Builder
	b.Grow(len(name))
	first := true
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || r == '_':
			b.WriteRune(r)
		case unicode.IsDigit(r) || r == '-' || r == '.':
			if first {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		case !first && (unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r)):
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
		first = false
	}
	return b.String()
}

// xmlNames resolves empty table or row names to the defaults and
// sanitizes both, shared by the collecting and streaming XML paths.
func xmlNames(tableName, rowName string) (string, string) {
	if tableName == "" {
		tableName = defaultTableName
	}
	if rowName == "" {
		rowName = defaultRowName
	}
	return sanitizeXMLName(tableName), sanitizeXMLName(rowName)
}

// RowsToXMLWithNames is the parameterized version of RowsToXML: it does
// not depend on the globals. Empty tableName or rowName fall back to the
// defaults, and table, row and column names are passed through
// sanitizeXMLName first, so names with spaces or a leading digit do not
// produce invalid XML, while valid non-ASCII names are kept as-is.
// Unlike the map APIs, this path preserves duplicate column names and
// their order, matching the legacy streaming output. Values are escaped
// by etree; characters outside the XML character range are rewritten by
// etree rather than passed through verbatim.
func RowsToXMLWithNames(rows *sql.Rows, tableName, rowName string) (string, error) {
	_, cellRows, err := scanStringCells(rows)
	if err != nil {
		return "", err
	}
	tableName, rowName = xmlNames(tableName, rowName)

	xmlDoc := etree.NewDocument()
	xmlDoc.CreateProcInst("xml", `version="1.0" encoding="UTF-8"`)
	rowData := xmlDoc.CreateElement(tableName)
	for rowCnt, cells := range cellRows {
		rowElement := rowData.CreateElement(fmt.Sprintf("%s%d", rowName, rowCnt))
		for _, cell := range cells {
			colElement := rowElement.CreateElement(sanitizeXMLName(cell.name))
			colElement.CreateText(cell.value)
		}
	}
	xmlDoc.Indent(2)
	return xmlDoc.WriteToString()
}

// WriteXML streams rows to w as XML, one row at a time, without
// accumulating the whole result. Empty tableName or rowName fall back
// to the defaults, names are sanitized as in RowsToXMLWithNames, and
// duplicate column names and their order are preserved. Unlike
// RowsToXML, the output is indented by encoding/xml rather than
// etree, so whitespace may differ while the element structure is the
// same. A mid-stream read or encode error cannot take back output
// already written, so the writer may be left with a partial document;
// the error is still returned. It does not close rows.
func WriteXML(w io.Writer, rows *sql.Rows, tableName, rowName string) error {
	if w == nil {
		return fmt.Errorf("gosqltool: nil writer")
	}
	if rows == nil {
		return fmt.Errorf("gosqltool: nil rows")
	}
	tableName, rowName = xmlNames(tableName, rowName)
	if _, err := w.Write([]byte(xml.Header)); err != nil {
		return fmt.Errorf("gosqltool: write: %w", err)
	}
	enc := xml.NewEncoder(w)
	enc.Indent("", "  ")
	encode := func(tok xml.Token) error {
		if err := enc.EncodeToken(tok); err != nil {
			return fmt.Errorf("gosqltool: xml: %w", err)
		}
		return nil
	}
	if err := encode(xml.StartElement{Name: xml.Name{Local: tableName}}); err != nil {
		return err
	}
	rowCnt := 0
	for cells, err := range iterCells(rows) {
		if err != nil {
			return err
		}
		rowElem := fmt.Sprintf("%s%d", rowName, rowCnt)
		rowCnt++
		if err := encode(xml.StartElement{Name: xml.Name{Local: rowElem}}); err != nil {
			return err
		}
		for _, cell := range cells {
			if err := encode(xml.StartElement{Name: xml.Name{Local: sanitizeXMLName(cell.name)}}); err != nil {
				return err
			}
			if err := encode(xml.CharData(cell.value)); err != nil {
				return err
			}
			if err := encode(xml.EndElement{Name: xml.Name{Local: sanitizeXMLName(cell.name)}}); err != nil {
				return err
			}
		}
		if err := encode(xml.EndElement{Name: xml.Name{Local: rowElem}}); err != nil {
			return err
		}
	}
	if err := encode(xml.EndElement{Name: xml.Name{Local: tableName}}); err != nil {
		return err
	}
	if err := enc.Flush(); err != nil {
		return fmt.Errorf("gosqltool: xml: %w", err)
	}
	return nil
}

// RowsToXML transfer sql res to xml
// <?xml version="1.0" encoding="UTF-8"?>
// <[tableName]> (default "RowData")
//
//	<[rowName]0> (default "Row_0")
//	    <[KEY1]>[VALUE1]</[KEY1]>
//	    <[KEY2]>[VALUE2]</[KEY2]>
//	</[rowName]0>
//
// </[tableName]>
//
// The table and row names come from the TableName and RowName globals;
// use RowsToXMLWithNames for per-call names.
// On a read error it returns "" and the error.
func RowsToXML(rows *sql.Rows) (xmlString string, err error) {
	return RowsToXMLWithNames(rows, TableName, RowName)
}
