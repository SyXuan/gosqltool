# gosqltool
Gosqltool help you to simply access sql result values that you don't know the keys. Otherwise, you don't need to declare struct and scan it.  
Features:  
* **RowsToMap**: Transfer sql rows to map[int]map[string]string
* **RowsToXML**: Transfer sql rows to XML String

## Install
```bash
go get github.com/syxuan/gosqltool
```

## Example
### RowsToMap
```go
    rows, err = db.Query("SELECT * FROM userinfo")
    defer rows.Close()
    checkErr(err)
    
    d, err := gosqltool.RowsToMap(rows)
    checkErr(err)
    fmt.Printf("%#v", d)
```
The output will be
```bash
map[int]map[string]string{4:map[string]string{"uid":"11", "username":"Trump_new", "departname":"Company", "created":"2018-03-20T00:00:00Z"}, 6:map[string]string{"uid":"13", "username":"Trump_new", "departname":"Company", "created":"2018-03-20T00:00:00Z"}}
```
And that you can easily get the value via map, such as
```go
    d[0]["username"]
```
### RowsToXML
XML is a very simple way to send data for web api or for windows executable dll files.
```go
    rows, err = db.Query("SELECT * FROM userinfo")
    defer rows.Close()
    checkErr(err)

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
  <Row_2>
    <uid>3</uid>
    <username>Trump_new</username>
    <departname>Company</departname>
    <created>2018-03-20T00:00:00Z</created>
  </Row_2>
</Data>
```

Or you can custom the TableName and RowName
```go
    rows, err = db.Query("SELECT * FROM userinfo")
    defer rows.Close()
    checkErr(err)

    gosqltool.TableName = "Table"
    gosqltool.RowName = "Row"

    d, err := gosqltool.RowsToXML(rows)
    checkErr(err)
    fmt.Println(d)
```
The output will be
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
  <Row2>
    <uid>3</uid>
    <username>Trump_new</username>
    <departname>Company</departname>
    <created>2018-03-20T00:00:00Z</created>
  </Row2>
</Table>
```
## Other features
To be continue.
## Contributing
This project accepts contributions. Just fork the repo and submit a pull request!