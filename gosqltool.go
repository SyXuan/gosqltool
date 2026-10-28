package gosqltool

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

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

// stringRows reads rows into an ordered slice of map[colName]value.
// NULL becomes an empty string, matching the legacy RowsToMap behaviour;
// use RowsToAny when NULL must stay distinguishable.
func stringRows(rows *sql.Rows) ([]string, []map[string]string, error) {
	if rows == nil {
		return nil, nil, fmt.Errorf("gosqltool: nil rows")
	}

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, err
	}
	if len(cols) == 0 {
		return nil, nil, fmt.Errorf("gosqltool: query returned no columns")
	}

	rawResult := make([][]byte, len(cols))
	dest := make([]any, len(cols))
	for i := range rawResult {
		dest[i] = &rawResult[i]
	}

	out := make([]map[string]string, 0)
	for rows.Next() {
		rowMap := make(map[string]string, len(cols))
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, err
		}
		for i, raw := range rawResult {
			// A nil raw value means NULL; the legacy behaviour is an empty string.
			rowMap[cols[i]] = string(raw)
		}
		out = append(out, rowMap)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
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
		return nil, err
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
			return nil, err
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
		return nil, err
	}
	return out, nil
}

// RowsToMap transfer sql res to map
// dataMap[rowCnt]map[colName]value, with rowCnt numbered from 0.
// On a read error it returns nil and the error, not a partial result.
// NULL becomes an empty string.
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
// and times are RFC3339. With no rows it returns "[]".
func RowsToJSON(rows *sql.Rows) (string, error) {
	slice, err := anyRows(rows)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(slice)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// sanitizeXMLName converts a column name into a valid XML element name:
// illegal characters become underscores, a leading digit, "-" or "."
// gets a "_" prefix, and an empty name becomes "col". Names that are
// already valid are left unchanged.
func sanitizeXMLName(name string) string {
	if name == "" {
		return "col"
	}
	var b strings.Builder
	b.Grow(len(name))
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_':
			b.WriteRune(r)
		case r >= '0' && r <= '9' || r == '-' || r == '.':
			if i == 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// RowsToXMLWithNames is the parameterized version of RowsToXML: it does
// not depend on the globals. Empty tableName or rowName fall back to the
// defaults, and column names are passed through sanitizeXMLName first,
// so names with spaces, non-ASCII characters or a leading digit do not
// produce invalid XML.
func RowsToXMLWithNames(rows *sql.Rows, tableName, rowName string) (string, error) {
	cols, slice, err := stringRows(rows)
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
	for rowCnt, rowMap := range slice {
		rowElement := rowData.CreateElement(fmt.Sprintf("%s%d", rowName, rowCnt))
		for _, col := range cols {
			colElement := rowElement.CreateElement(sanitizeXMLName(col))
			colElement.CreateText(rowMap[col])
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
