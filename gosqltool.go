package gosqltool

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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

// scanStringCells reads rows into ordered per-row cells. NULL becomes an
// empty string, matching the legacy RowsToMap behaviour; use RowsToAny
// when NULL must stay distinguishable.
func scanStringCells(rows *sql.Rows) ([]string, [][]stringCell, error) {
	if rows == nil {
		return nil, nil, fmt.Errorf("gosqltool: nil rows")
	}

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, fmt.Errorf("gosqltool: columns: %w", err)
	}
	if len(cols) == 0 {
		return nil, nil, fmt.Errorf("gosqltool: query returned no columns")
	}

	rawResult := make([][]byte, len(cols))
	dest := make([]any, len(cols))
	for i := range rawResult {
		dest[i] = &rawResult[i]
	}

	out := make([][]stringCell, 0)
	for rows.Next() {
		cells := make([]stringCell, len(cols))
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, fmt.Errorf("gosqltool: scan: %w", err)
		}
		for i, raw := range rawResult {
			// A nil raw value means NULL; the legacy behaviour is an empty string.
			cells[i] = stringCell{name: cols[i], value: string(raw)}
		}
		out = append(out, cells)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("gosqltool: rows: %w", err)
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

// anyRows reads rows into an ordered slice of map[colName]value.
// Driver types are preserved where possible (int64, float64, bool,
// time.Time, ...); []byte is converted to string so JSON encoding does not
// turn it into base64, and NULL becomes nil.
func anyRows(rows *sql.Rows) ([]map[string]any, error) {
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

	vals := make([]any, len(cols))
	dest := make([]any, len(cols))
	for i := range vals {
		dest[i] = &vals[i]
	}
	out := make([]map[string]any, 0)
	for rows.Next() {
		rowMap := make(map[string]any, len(cols))
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("gosqltool: scan: %w", err)
		}
		for i, v := range vals {
			if b, ok := v.([]byte); ok {
				rowMap[cols[i]] = string(b)
			} else {
				rowMap[cols[i]] = v
			}
		}
		out = append(out, rowMap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("gosqltool: rows: %w", err)
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
// returns an empty, non-nil slice.
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
	if tableName == "" {
		tableName = defaultTableName
	}
	if rowName == "" {
		rowName = defaultRowName
	}
	tableName = sanitizeXMLName(tableName)
	rowName = sanitizeXMLName(rowName)

	xml := etree.NewDocument()
	xml.CreateProcInst("xml", `version="1.0" encoding="UTF-8"`)
	rowData := xml.CreateElement(tableName)
	for rowCnt, cells := range cellRows {
		rowElement := rowData.CreateElement(fmt.Sprintf("%s%d", rowName, rowCnt))
		for _, cell := range cells {
			colElement := rowElement.CreateElement(sanitizeXMLName(cell.name))
			colElement.CreateText(cell.value)
		}
	}
	xml.Indent(2)
	return xml.WriteToString()
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
