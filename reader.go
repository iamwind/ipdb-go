package ipdb

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net"
	"os"
	"reflect"
	"strings"
	"time"
	"unsafe"
	"fmt"
)

const IPv4 = 0x01
const IPv6 = 0x02

var (
	ErrFileSize = errors.New("IP Database file size error.")
	ErrMetaData = errors.New("IP Database metadata error.")
	ErrReadFull = errors.New("IP Database ReadFull error.")

	ErrDatabaseError = errors.New("database error")

	ErrIPFormat = errors.New("Query IP Format error.")

	ErrNoSupportLanguage = errors.New("language not support")
	ErrNoSupportIPv4     = errors.New("IPv4 not support")
	ErrNoSupportIPv6     = errors.New("IPv6 not support")

	ErrDataNotExists = errors.New("data is not exists")
)

type MetaData struct {
	Build     int64          `json:"build"`
	IPVersion uint16         `json:"ip_version"`
	Languages map[string]int `json:"languages"`
	NodeCount int            `json:"node_count"`
	TotalSize int            `json:"total_size"`
	Fields    []string       `json:"fields"`
}

type reader struct {
	fileSize  int
	nodeCount int
	v4offset  int

	meta MetaData
	data []byte

	refType map[string]string
}

func newReader(name string, obj interface{}) (*reader, error) {
	var err error
	var fileInfo os.FileInfo
	fileInfo, err = os.Stat(name)
	if err != nil {
		return nil, err
	}
	fileSize := int(fileInfo.Size())
	if fileSize < 4 {
		return nil, ErrFileSize
	}
	body, err := ioutil.ReadFile(name)
	if err != nil {
		return nil, ErrReadFull
	}
	var meta MetaData
	metaLength := int(binary.BigEndian.Uint32(body[0:4]))
	if fileSize < (4 + metaLength) {
		return nil, ErrFileSize
	}
	if err := json.Unmarshal(body[4:4+metaLength], &meta); err != nil {
		return nil, err
	}
	if len(meta.Languages) == 0 || len(meta.Fields) == 0 {
		return nil, ErrMetaData
	}
	if fileSize != (4 + metaLength + meta.TotalSize) {
		return nil, ErrFileSize
	}

	var dm map[string]string
	if obj != nil {
		t := reflect.TypeOf(obj).Elem()
		dm = make(map[string]string, t.NumField())
		for i := 0; i < t.NumField(); i++ {
			k := t.Field(i).Tag.Get("json")
			dm[k] = t.Field(i).Name
		}
	}

	db := &reader{
		fileSize:  fileSize,
		nodeCount: meta.NodeCount,

		meta:    meta,
		refType: dm,

		data: body[4+metaLength:],
	}

	if db.v4offset == 0 {
		node := 0
		for i := 0; i < 96 && node < db.nodeCount; i++ {
			if i >= 80 {
				node = db.readNode(node, 1)
			} else {
				node = db.readNode(node, 0)
			}
		}
		db.v4offset = node
	}

	return db, nil
}

func (db *reader) Find(addr, language string) ([]string, error) {
	return db.find1(addr, language)
}

func (db *reader) FindMap(addr, language string) (map[string]string, error) {

	data, err := db.find1(addr, language)
	if err != nil {
		return nil, err
	}
	info := make(map[string]string, len(db.meta.Fields))
	for k, v := range data {
		info[db.meta.Fields[k]] = v
	}

	return info, nil
}

func (db *reader) find0(addr string) ([]byte, error) {

	var err error
	var node int
	ipv := net.ParseIP(addr)
	if ip := ipv.To4(); ip != nil {
		if !db.IsIPv4Support() {
			return nil, ErrNoSupportIPv4
		}

		node, err = db.search(ip, 32)
	} else if ip := ipv.To16(); ip != nil {
		if !db.IsIPv6Support() {
			return nil, ErrNoSupportIPv6
		}

		node, err = db.search(ip, 128)
	} else {
		return nil, ErrIPFormat
	}

	if err != nil || node < 0 {
		return nil, err
	}

	body, err := db.resolve(node)
	if err != nil {
		return nil, err
	}

	return body, nil
}

func (db *reader) find1(addr, language string) ([]string, error) {

	off, ok := db.meta.Languages[language]
	if !ok {
		return nil, ErrNoSupportLanguage
	}

	body, err := db.find0(addr)
	if err != nil {
		return nil, err
	}

	str := (*string)(unsafe.Pointer(&body))
	tmp := strings.Split(*str, "\t")

	if (off + len(db.meta.Fields)) > len(tmp) {
		return nil, ErrDatabaseError
	}

	return tmp[off : off+len(db.meta.Fields)], nil
}

func (db *reader) writeTXT(language string) (error){
	var node int
	var lastnode int
	bitCount := uint(32)
	
	var start int64
	var end int64
	var laststart int64
	var lastend int64

	start = 0
	lastnode = db.v4offset
	laststart = 0
	lastend = 0

	//loop from 0 to 2^32-1
	for end=0; end<(2<<bitCount); {
		node = db.v4offset

		//convert uint32 to byte[]
		b := make([]byte, 4)
		binary.BigEndian.PutUint32(b, uint32(end))
	
		ip := net.IPv4(b[0], b[1], b[2], b[3]).To4()
		//info, _ := db.Find(ip.String(), "CN")
		//nodetmp, _ := db.search(ip, 32)
		//fmt.Printf("ip: %s %v %s\n", ip.String(), nodetmp, info)
		//fmt.Printf("end:%s\n", ip.String())
		
		//db.search()
		for i := uint(0); i <= bitCount; i++ {
			if node > db.nodeCount {				
				end = end>>(bitCount-i)<<(bitCount-i) + 1 << (bitCount-i) - 1
				//find first
				if lastnode == db.v4offset {
					lastend = end
					lastnode = node
					end++
					break
				}

				//if node is same
				if lastnode == node {
					lastend = end
					lastnode = node
					end++
					break
				}
				
				body, _ := db.resolve(lastnode)

				off, ok := db.meta.Languages[language]
				if !ok {
					return ErrNoSupportLanguage
				}

				str := (*string)(unsafe.Pointer(&body))
				tmp := strings.Split(*str, "\t")

				if (off + len(db.meta.Fields)) > len(tmp) {
					return ErrDatabaseError
				}

				fmt.Printf("%s\t%s\t%s\n",
					fmt.Sprintf("%d.%d.%d.%d", byte(laststart>>24), byte(laststart>>16), byte(laststart>>8), byte(laststart)),
					fmt.Sprintf("%d.%d.%d.%d", byte(lastend>>24), byte(lastend>>16), byte(lastend>>8), byte(lastend)),
					tmp[off : off+len(db.meta.Fields)])

				start = lastend + 1
				laststart = start
				lastend = end
				lastnode = node
				
				end ++
				break
			}

			//i>>3: divide index of IP's bit by 8, for get index of IP's byte
			//0xFF&(ip[i>>3]): get just on byte
			//>>uint(7-(i%8)): trim surffix bit
			//&1: get just one bit
			node = db.readNode(node, ((0xFF&int(ip[i>>3]))>>uint(7-(i%8)))&1)
		}
	}

	return nil
}

func (db *reader) search(ip net.IP, bitCount int) (int, error) {

	var node int

	if bitCount == 32 {
		node = db.v4offset
	} else {
		node = 0
	}

	for i := 0; i < bitCount; i++ {
		if node > db.nodeCount {
			break
		}
		//i>>3: divide index of IP's bit by 8, for get index of IP's byte
		//0xFF&(ip[i>>3]): get just on byte
		//>>uint(7-(i%8)): trim surffix bit
		//&1: get just one bit
		node = db.readNode(node, ((0xFF&int(ip[i>>3]))>>uint(7-(i%8)))&1)
	}

	if node > db.nodeCount {
		return node, nil
	}

	return -1, ErrDataNotExists
}

func (db *reader) readNode(node, index int) int {
	off := node*8 + index*4
	return int(binary.BigEndian.Uint32(db.data[off : off+4]))
}

func (db *reader) resolve(node int) ([]byte, error) {
	resolved := node - db.nodeCount + db.nodeCount*8
	if resolved >= db.fileSize {
		return nil, ErrDatabaseError
	}

	size := int(binary.BigEndian.Uint16(db.data[resolved : resolved+2]))
	if (resolved + 2 + size) > len(db.data) {
		return nil, ErrDatabaseError
	}
	bytes := db.data[resolved+2 : resolved+2+size]

	return bytes, nil
}

func (db *reader) IsIPv4Support() bool {
	return (int(db.meta.IPVersion) & IPv4) == IPv4
}

func (db *reader) IsIPv6Support() bool {
	return (int(db.meta.IPVersion) & IPv6) == IPv6
}

func (db *reader) Build() time.Time {
	return time.Unix(db.meta.Build, 0).In(time.UTC)
}

func (db *reader) Languages() []string {
	ls := make([]string, 0, len(db.meta.Languages))
	for k := range db.meta.Languages {
		ls = append(ls, k)
	}
	return ls
}
