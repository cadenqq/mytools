package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

type Connect struct {
	req *http.Request
	res *http.Response
}

// Burp History XML이 파싱되어 저장될 구조체
type Items struct {
	XMLName     xml.Name `xml:"items"`
	Text        string   `xml:",chardata"`
	BurpVersion string   `xml:"burpVersion,attr"`
	ExportTime  string   `xml:"exportTime,attr"`
	Item        []struct {
		Text string `xml:",chardata"`
		Time string `xml:"time"`
		URL  string `xml:"url"`
		Host struct {
			Text string `xml:",chardata"`
			Ip   string `xml:"ip,attr"`
		} `xml:"host"`
		Port      string `xml:"port"`
		Protocol  string `xml:"protocol"`
		Method    string `xml:"method"`
		Path      string `xml:"path"`
		Extension string `xml:"extension"`
		Request   struct {
			Text   string `xml:",chardata"`
			Base64 string `xml:"base64,attr"`
		} `xml:"request"`
		Status         string `xml:"status"`
		Responselength string `xml:"responselength"`
		Mimetype       string `xml:"mimetype"`
		Response       struct {
			Text   string `xml:",chardata"`
			Base64 string `xml:"base64,attr"`
		} `xml:"response"`
		Comment string `xml:"comment"`
	} `xml:"item"`
}

func main() {
	file := flag.String("f", "", "Add XML file containing Burp Suite history")
	header := flag.String("h", "", "Add header to auth testing.")
	flag.Parse()

	if file == nil {
		fmt.Fprintf(os.Stderr, "Usage : %s -f <file-path> [-h <string>]\n", os.Args[0])
		flag.PrintDefaults()
		return
	}

	//Burp suite 히스토리 파일 파싱
	reqs := ParseBurpHist(*file)
	fmt.Println("--------------------------------------------------------")

	// 파싱하여 얻은 Request 정보로 실제 요청을 고루틴으로 수행
	conns := Req(reqs, *header)

	// 채널로 받은 응답 값 출력
	for i := 0; i < len(conns); i++ {
		if conns[i].res != nil {
			fmt.Printf("[%s] (%s) %s\n", conns[i].res.Status, conns[i].req.Method, conns[i].req.URL)
		}
	}

	fmt.Println("--------------------------------------------------------")
	fmt.Printf("%d request are processed!\n", len(conns))

}

// Burp suite 히스토리 파일 파싱
func ParseBurpHist(file string) []*http.Request {
	// Burp History에서 추출한 Request를 저장할 슬라이스
	reqs := []*http.Request{}

	// Burp History XML이 파싱되어 저장될 구조체
	var items Items

	xmlFile, err := os.Open(file)
	if err != nil {
		log.Fatal("[!] Input correct file path, Please.")
		panic(err)
	}

	defer xmlFile.Close()
	fmt.Printf("[*] Successfully Opened %s\n", file)

	byteValue, err := io.ReadAll(xmlFile)
	if err != nil {
		log.Fatal("[!] Cannot read file.")
		panic(err)
	}

	xml.Unmarshal(byteValue, &items)

	fmt.Println("[*] URLs in file ")
	//파싱된 XML에서 Request가 있는 item 요소만 추출
	for i := 0; i < len(items.Item); i++ {
		data, err := base64.StdEncoding.DecodeString(items.Item[i].Request.Text)
		if err != nil {
			log.Fatal("[!] Cannot decode contents in file.")
			panic(err)
		}

		// readRequest 함수 인자로 넣기위해 []byte를 bufio.Reader로 형 변환
		reqData := bufio.NewReader(bytes.NewReader(data))

		//ReadRequest로 HTTP Request 패킷을 파싱하여 requst 객체로 리턴
		req, err := http.ReadRequest(reqData)
		if err != nil {
			log.Fatal("[!] Cannot parse request.")
			panic(err)
		}

		// req (http.Request) 에 URL 경로 값이 아닌, Full URL을 대입
		// req.RequestURI 값이 있을 경우, http.Request.URL 보다 우선해서 쓰이므로 빈 값으로 초기화
		req.RequestURI = ""

		req.URL, err = url.Parse(items.Item[i].URL)
		if err != nil {
			log.Fatal("[!] Cannot parse request in file.")
			panic(err)
		}

		reqs = append(reqs, req)

		fmt.Printf("    (%s) %s\n", reqs[i].Method, req.URL)
	}

	return reqs
}

// *http.Request 슬라이스를 받아 고루틴으로 요청 실행 후, Connect 구조체에 Request, Response를 채워 반환
func Req(reqs []*http.Request, header string) []Connect {
	defer func() {
		if r := recover(); r != nil {
			log.Fatal("Recovering from panic:", r)
		}
	}()

	runtime.GOMAXPROCS(runtime.NumCPU())
	wg := new(sync.WaitGroup)

	// Request에 대한 Respone 쌍을 담을 Connect 구조체 슬라이스, mill로 초기화
	conns := make([]Connect, len(reqs))

	// 인증 값 넣을 헤더가 있을 경우 대입, 없을 경우 추가
	headerSet := strings.Split(header, ":")
	headerName := strings.Trim(headerSet[0], " ")
	headerData := strings.Trim(headerSet[1], " ")

	for i := 0; i < len(reqs); i++ {
		if _, ok := reqs[i].Header[headerName]; ok {
			reqs[i].Header[headerName] = []string{headerData}
		} else {
			reqs[i].Header.Add(headerName, headerData)
		}
	}

	// HTTP 연결 수 제한 설정
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 10
	t.MaxConnsPerHost = 5
	t.MaxIdleConnsPerHost = 10

	// 클라이언트 생성
	client := &http.Client{
		Timeout:   5 * time.Second,
		Transport: t,
	}

	for i := 0; i < len(conns); i++ {
		wg.Add(1)
		go func(i int) {
			resp, err := client.Do(reqs[i])
			if err != nil {
				panic(err)
			}
			resp.Body.Close()

			conns[i] = Connect{reqs[i], resp}
			wg.Done()
		}(i)
	}

	wg.Wait()
	return conns
}
