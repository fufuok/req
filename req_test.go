package req

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/imroc/req/v3/internal/tests"
	"go/token"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"
)

func tc() *Client {
	return C().
		SetBaseURL(getTestServerURL()).
		EnableInsecureSkipVerify()
}

var testDataPath string

func init() {
	pwd, _ := os.Getwd()
	testDataPath = filepath.Join(pwd, ".testdata")
}

func createTestServer() *httptest.Server {
	server := httptest.NewUnstartedServer(http.HandlerFunc(handleHTTP))
	server.EnableHTTP2 = true
	server.StartTLS()
	return server
}

func handleHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Add("Method", r.Method)
	switch r.Method {
	case http.MethodGet:
		handleGet(w, r)
	case http.MethodPost:
		handlePost(w, r)
	}
}

var testServerMu sync.Mutex
var testServer *httptest.Server

func getTestServerURL() string {
	if testServer != nil {
		return testServer.URL
	}
	testServerMu.Lock()
	defer testServerMu.Unlock()
	testServer = createTestServer()
	return testServer.URL
}

type echo struct {
	Header http.Header `json:"header" xml:"header"`
	Body   string      `json:"body" xml:"body"`
}

func handlePost(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		io.Copy(ioutil.Discard, r.Body)
		w.Write([]byte("TestPost: text response"))
	case "/raw-upload":
		io.Copy(ioutil.Discard, r.Body)
	case "/file-text":
		r.ParseMultipartForm(10e6)
		files := r.MultipartForm.File["file"]
		file, _ := files[0].Open()
		b, _ := ioutil.ReadAll(file)
		w.Write(b)
	case "/form":
		r.ParseForm()
		ret, _ := json.Marshal(&r.Form)
		w.Header().Set(hdrContentTypeKey, jsonContentType)
		w.Write(ret)
	case "/multipart":
		r.ParseMultipartForm(10e6)
		m := make(map[string]interface{})
		m["values"] = r.MultipartForm.Value
		m["files"] = r.MultipartForm.File
		ret, _ := json.Marshal(&m)
		w.Header().Set(hdrContentTypeKey, jsonContentType)
		w.Write(ret)
	case "/search":
		handleSearch(w, r)
	case "/redirect":
		io.Copy(ioutil.Discard, r.Body)
		w.Header().Set(hdrLocationKey, "/")
		w.WriteHeader(http.StatusMovedPermanently)
	case "/content-type":
		io.Copy(ioutil.Discard, r.Body)
		w.Write([]byte(r.Header.Get(hdrContentTypeKey)))
	case "/echo":
		b, _ := ioutil.ReadAll(r.Body)
		e := echo{
			Header: r.Header,
			Body:   string(b),
		}
		w.Header().Set(hdrContentTypeKey, jsonContentType)
		result, _ := json.Marshal(&e)
		w.Write(result)
	}
}

func handleGetUserProfile(w http.ResponseWriter, r *http.Request) {
	user := strings.TrimLeft(r.URL.Path, "/user")
	user = strings.TrimSuffix(user, "/profile")
	w.Write([]byte(fmt.Sprintf("%s's profile", user)))
}

type UserInfo struct {
	Username string `json:"username" xml:"username"`
	Email    string `json:"email" xml:"email"`
}

type ErrorMessage struct {
	ErrorCode    int    `json:"error_code" xml:"ErrorCode"`
	ErrorMessage string `json:"error_message" xml:"ErrorMessage"`
}

func handleSearch(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()
	username := r.FormValue("username")
	tp := r.FormValue("type")
	var marshalFunc func(v interface{}) ([]byte, error)
	if tp == "xml" {
		w.Header().Set(hdrContentTypeKey, xmlContentType)
		marshalFunc = xml.Marshal
	} else {
		w.Header().Set(hdrContentTypeKey, jsonContentType)
		marshalFunc = json.Marshal
	}
	var result interface{}
	switch username {
	case "":
		w.WriteHeader(http.StatusBadRequest)
		result = &ErrorMessage{
			ErrorCode:    10000,
			ErrorMessage: "need username",
		}
	case "imroc":
		w.WriteHeader(http.StatusOK)
		result = &UserInfo{
			Username: "imroc",
			Email:    "roc@imroc.cc",
		}
	default:
		w.WriteHeader(http.StatusNotFound)
		result = &ErrorMessage{
			ErrorCode:    10001,
			ErrorMessage: "username not exists",
		}
	}
	data, _ := marshalFunc(result)
	w.Write(data)
}

func toGbk(s string) []byte {
	reader := transform.NewReader(strings.NewReader(s), simplifiedchinese.GBK.NewEncoder())
	d, e := ioutil.ReadAll(reader)
	if e != nil {
		panic(e)
	}
	return d
}

func handleGet(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/":
		w.Write([]byte("TestGet: text response"))
	case "/bad-request":
		w.WriteHeader(http.StatusBadRequest)
	case "/too-many":
		w.WriteHeader(http.StatusTooManyRequests)
		w.Header().Set(hdrContentTypeKey, jsonContentType)
		w.Write([]byte(`{"errMsg":"too many requests"}`))
	case "/retry":
		r.ParseForm()
		r.Form.Get("attempt")
	case "/chunked":
		w.Header().Add("Trailer", "Expires")
		w.Write([]byte(`This is a chunked body`))
	case "/host-header":
		w.Write([]byte(r.Host))
	case "/json":
		r.ParseForm()
		if r.FormValue("type") != "no" {
			w.Header().Set(hdrContentTypeKey, jsonContentType)
		}
		w.Header().Set(hdrContentTypeKey, jsonContentType)
		if r.FormValue("error") == "yes" {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"message": "not allowed"}`))
		} else {
			w.Write([]byte(`{"name": "roc"}`))
		}
	case "/xml":
		r.ParseForm()
		if r.FormValue("type") != "no" {
			w.Header().Set(hdrContentTypeKey, xmlContentType)
		}
		w.Write([]byte(`<user><name>roc</name></user>`))
	case "/unlimited-redirect":
		w.Header().Set("Location", "/unlimited-redirect")
		w.WriteHeader(http.StatusMovedPermanently)
	case "/redirect-to-other":
		w.Header().Set("Location", "http://dummy.local/test")
		w.WriteHeader(http.StatusMovedPermanently)
	case "/pragma":
		w.Header().Add("Pragma", "no-cache")
	case "/payload":
		b, _ := ioutil.ReadAll(r.Body)
		w.Write(b)
	case "/gbk":
		w.Header().Set(hdrContentTypeKey, "text/plain; charset=gbk")
		w.Write(toGbk("我是roc"))
	case "/gbk-no-charset":
		b, err := ioutil.ReadFile(tests.GetTestFilePath("sample-gbk.html"))
		if err != nil {
			panic(err)
		}
		w.Header().Set(hdrContentTypeKey, "text/html")
		w.Write(b)
	case "/header":
		b, _ := json.Marshal(r.Header)
		w.Header().Set(hdrContentTypeKey, jsonContentType)
		w.Write(b)
	case "/user-agent":
		w.Write([]byte(r.Header.Get(hdrUserAgentKey)))
	case "/content-type":
		w.Write([]byte(r.Header.Get(hdrContentTypeKey)))
	case "/query-parameter":
		w.Write([]byte(r.URL.RawQuery))
	case "/search":
		handleSearch(w, r)
	default:
		if strings.HasPrefix(r.URL.Path, "/user") {
			handleGetUserProfile(w, r)
		}
	}
}

func assertStatus(t *testing.T, resp *Response, err error, statusCode int, status string) {
	assertError(t, err)
	assertNotNil(t, resp)
	assertNotNil(t, resp.Body)
	assertEqual(t, statusCode, resp.StatusCode)
	assertEqual(t, status, resp.Status)
}

func assertSuccess(t *testing.T, resp *Response, err error) {
	assertError(t, err)
	assertNotNil(t, resp.Response)
	assertNotNil(t, resp.Response.Body)
	assertEqual(t, http.StatusOK, resp.StatusCode)
	assertEqual(t, "200 OK", resp.Status)
	if !resp.IsSuccess() {
		t.Error("Response.IsSuccess should return true")
	}
}

func assertIsError(t *testing.T, resp *Response, err error) {
	assertError(t, err)
	assertNotNil(t, resp)
	assertNotNil(t, resp.Body)
	if !resp.IsError() {
		t.Error("Response.IsError should return true")
	}
}

func assertNil(t *testing.T, v interface{}) {
	if !isNil(v) {
		t.Errorf("[%v] was expected to be nil", v)
	}
}

func assertNotNil(t *testing.T, v interface{}) {
	if isNil(v) {
		t.Fatalf("[%v] was expected to be non-nil", v)
	}
}

func assertError(t *testing.T, err error) {
	if err != nil {
		t.Errorf("Error occurred [%v]", err)
	}
}

func assertEqualStruct(t *testing.T, e, g interface{}, onlyExported bool, excludes ...string) {
	ev := reflect.ValueOf(e).Elem()
	gv := reflect.ValueOf(g).Elem()
	et := ev.Type()
	gt := gv.Type()
	m := map[string]bool{}
	for _, exclude := range excludes {
		m[exclude] = true
	}
	if et.Kind() != reflect.Struct {
		t.Fatalf("expect object should be struct instead of %v", et.Kind().String())
	}

	if gt.Kind() != reflect.Struct {
		t.Fatalf("got object should be struct instead of %v", gt.Kind().String())
	}

	if et.Name() != gt.Name() {
		t.Fatalf("Expected type [%s], got [%s]", et.Name(), gt.Name())
	}

	for i := 0; i < ev.NumField(); i++ {
		sf := ev.Field(i)
		if sf.Kind() == reflect.Func || sf.Kind() == reflect.Slice {
			continue
		}
		st := et.Field(i)
		if m[st.Name] {
			continue
		}
		if onlyExported && !token.IsExported(st.Name) {
			continue
		}
		var ee, gg interface{}
		if !token.IsExported(st.Name) {
			ee = reflect.NewAt(sf.Type(), unsafe.Pointer(sf.UnsafeAddr())).Elem().Interface()
			gg = reflect.NewAt(sf.Type(), unsafe.Pointer(gv.Field(i).UnsafeAddr())).Elem().Interface()
		} else {
			ee = sf.Interface()
			gg = gv.Field(i).Interface()
		}
		if !reflect.DeepEqual(ee, gg) {
			t.Errorf("Field %s.%s is not equal, expected [%v], got [%v]", et.Name(), et.Field(i).Name, ee, gg)
		}
	}

}

func assertEqual(t *testing.T, e, g interface{}) {
	if !equal(e, g) {
		t.Errorf("Expected [%+v], got [%+v]", e, g)
	}
	return
}

func assertNotEqual(t *testing.T, e, g interface{}) (r bool) {
	if equal(e, g) {
		t.Errorf("Expected [%v], got [%v]", e, g)
	} else {
		r = true
	}
	return
}

func equal(expected, got interface{}) bool {
	return reflect.DeepEqual(expected, got)
}

func isNil(v interface{}) bool {
	if v == nil {
		return true
	}
	rv := reflect.ValueOf(v)
	kind := rv.Kind()
	if kind >= reflect.Chan && kind <= reflect.Slice && rv.IsNil() {
		return true
	}
	return false
}

func testGlobalWrapperEnableDumps(t *testing.T) {
	testGlobalWrapperEnableDump(t, func(reqHeader, reqBody, respHeader, respBody *bool) *Request {
		*reqHeader = true
		*reqBody = true
		*respHeader = true
		*respBody = true
		return EnableDump()
	})

	testGlobalWrapperEnableDump(t, func(reqHeader, reqBody, respHeader, respBody *bool) *Request {
		*reqHeader = false
		*reqBody = false
		*respHeader = true
		*respBody = true
		return EnableDumpWithoutRequest()
	})

	testGlobalWrapperEnableDump(t, func(reqHeader, reqBody, respHeader, respBody *bool) *Request {
		*reqHeader = true
		*reqBody = false
		*respHeader = true
		*respBody = true
		return EnableDumpWithoutRequestBody()
	})

	testGlobalWrapperEnableDump(t, func(reqHeader, reqBody, respHeader, respBody *bool) *Request {
		*reqHeader = true
		*reqBody = true
		*respHeader = false
		*respBody = false
		return EnableDumpWithoutResponse()
	})

	testGlobalWrapperEnableDump(t, func(reqHeader, reqBody, respHeader, respBody *bool) *Request {
		*reqHeader = true
		*reqBody = true
		*respHeader = true
		*respBody = false
		return EnableDumpWithoutResponseBody()
	})

	testGlobalWrapperEnableDump(t, func(reqHeader, reqBody, respHeader, respBody *bool) *Request {
		*reqHeader = false
		*reqBody = true
		*respHeader = false
		*respBody = true
		return EnableDumpWithoutHeader()
	})

	testGlobalWrapperEnableDump(t, func(reqHeader, reqBody, respHeader, respBody *bool) *Request {
		*reqHeader = true
		*reqBody = false
		*respHeader = true
		*respBody = false
		return EnableDumpWithoutBody()
	})

	buf := new(bytes.Buffer)
	r := EnableDumpTo(buf)
	assertEqual(t, true, r.getDumpOptions().Output != nil)

	dumpFile := tests.GetTestFilePath("req_tmp_dump.out")
	r = EnableDumpToFile(tests.GetTestFilePath(dumpFile))
	assertEqual(t, true, r.getDumpOptions().Output != nil)
	os.Remove(dumpFile)

	r = SetDumpOptions(&DumpOptions{
		RequestHeader: true,
	})
	assertEqual(t, true, r.getDumpOptions().RequestHeader)
}

func testGlobalWrapperEnableDump(t *testing.T, fn func(reqHeader, reqBody, respHeader, respBody *bool) *Request) {
	var reqHeader, reqBody, respHeader, respBody bool
	r := fn(&reqHeader, &reqBody, &respHeader, &respBody)
	dump, ok := r.Context().Value(dumperKey).(*dumper)
	if !ok {
		t.Fatal("no dumper found in request context")
	}
	if reqHeader != dump.DumpOptions.RequestHeader {
		t.Errorf("Unexpected RequestHeader dump option, expected [%v], got [%v]", reqHeader, dump.DumpOptions.RequestHeader)
	}
	if reqBody != dump.DumpOptions.RequestBody {
		t.Errorf("Unexpected RequestBody dump option, expected [%v], got [%v]", reqBody, dump.DumpOptions.RequestBody)
	}
	if respHeader != dump.DumpOptions.ResponseHeader {
		t.Errorf("Unexpected RequestHeader dump option, expected [%v], got [%v]", respHeader, dump.DumpOptions.ResponseHeader)
	}
	if respBody != dump.DumpOptions.ResponseBody {
		t.Errorf("Unexpected RequestHeader dump option, expected [%v], got [%v]", respBody, dump.DumpOptions.ResponseBody)
	}
}

func testGlobalWrapperSendRequest(t *testing.T) {
	testURL := getTestServerURL() + "/"

	resp, err := Put(testURL)
	assertSuccess(t, resp, err)
	assertEqual(t, "PUT", resp.Header.Get("Method"))
	resp = MustPut(testURL)
	assertEqual(t, "PUT", resp.Header.Get("Method"))

	resp, err = Patch(testURL)
	assertSuccess(t, resp, err)
	assertEqual(t, "PATCH", resp.Header.Get("Method"))
	resp = MustPatch(testURL)
	assertEqual(t, "PATCH", resp.Header.Get("Method"))

	resp, err = Delete(testURL)
	assertSuccess(t, resp, err)
	assertEqual(t, "DELETE", resp.Header.Get("Method"))
	resp = MustDelete(testURL)
	assertEqual(t, "DELETE", resp.Header.Get("Method"))

	resp, err = Options(testURL)
	assertSuccess(t, resp, err)
	assertEqual(t, "OPTIONS", resp.Header.Get("Method"))
	resp = MustOptions(testURL)
	assertEqual(t, "OPTIONS", resp.Header.Get("Method"))

	resp, err = Head(testURL)
	assertSuccess(t, resp, err)
	assertEqual(t, "HEAD", resp.Header.Get("Method"))
	resp = MustHead(testURL)
	assertEqual(t, "HEAD", resp.Header.Get("Method"))

	resp, err = Get(testURL)
	assertSuccess(t, resp, err)
	assertEqual(t, "GET", resp.Header.Get("Method"))
	resp = MustGet(testURL)
	assertEqual(t, "GET", resp.Header.Get("Method"))

	resp, err = Post(testURL)
	assertSuccess(t, resp, err)
	assertEqual(t, "POST", resp.Header.Get("Method"))
	resp = MustPost(testURL)
	assertEqual(t, "POST", resp.Header.Get("Method"))
}

func testGlobalWrapperSetRequest(t *testing.T, rs ...*Request) {
	for _, r := range rs {
		assertNotNil(t, r)
	}
}

func TestGlobalWrapperSetRequest(t *testing.T) {
	SetLogger(nil)
	testFilePath := tests.GetTestFilePath("sample-file.txt")
	values := make(url.Values)
	values.Add("test", "test")
	testGlobalWrapperSetRequest(t,
		SetFiles(map[string]string{"test": testFilePath}),
		SetFile("test", tests.GetTestFilePath("sample-file.txt")),
		SetFile("test", tests.GetTestFilePath("file-not-exists.txt")),
		SetFileReader("test", "test.txt", bytes.NewBufferString("test")),
		SetFileBytes("test", "test.txt", []byte("test")),
		SetFileUpload(FileUpload{ParamName: "test", FileName: "test.txt", GetFileContent: func() (io.ReadCloser, error) {
			return nil, nil
		}}),
		SetError(&ErrorMessage{}),
		SetResult(&UserInfo{}),
		SetOutput(nil),
		SetOutput(bytes.NewBufferString("test")),
		SetHeader("test", "test"),
		SetHeaders(map[string]string{"test": "test"}),
		SetCookies(&http.Cookie{
			Name:  "test",
			Value: "test",
		}),
		SetBasicAuth("imroc", "123456"),
		SetBearerAuthToken("123456"),
		SetQueryString("test=test"),
		SetQueryString("ksjlfjk?"),
		SetQueryParam("test", "test"),
		AddQueryParam("test", "test"),
		SetQueryParams(map[string]string{"test": "test"}),
		SetPathParam("test", "test"),
		SetPathParams(map[string]string{"test": "test"}),
		SetFormData(map[string]string{"test": "test"}),
		SetFormDataFromValues(values),
		SetContentType(jsonContentType),
		AddRetryCondition(func(rep *Response, err error) bool {
			return err != nil
		}),
		SetRetryCondition(func(rep *Response, err error) bool {
			return err != nil
		}),
		AddRetryHook(func(resp *Response, err error) {}),
		SetRetryHook(func(resp *Response, err error) {}),
		SetRetryBackoffInterval(1*time.Millisecond, 500*time.Millisecond),
		SetRetryFixedInterval(1*time.Millisecond),
		SetRetryInterval(func(resp *Response, attempt int) time.Duration {
			return 1 * time.Millisecond
		}),
		SetRetryCount(3),
		SetBodyXmlMarshal(0),
		SetBodyString("test"),
		SetBodyBytes([]byte("test")),
		SetBodyJsonBytes([]byte(`{"user":"roc"}`)),
		SetBodyJsonString(`{"user":"roc"}`),
		SetBodyXmlBytes([]byte("test")),
		SetBodyXmlString("test"),
		SetBody("test"),
		SetBodyJsonMarshal(User{
			Name: "roc",
		}),
		EnableTrace(),
		DisableTrace(),
		SetContext(context.Background()),
	)
}

func testGlobalClientSettingWrapper(t *testing.T, cs ...*Client) {
	for _, c := range cs {
		assertNotNil(t, c)
	}
}

func TestGlobalWrapper(t *testing.T) {
	EnableInsecureSkipVerify()
	testGlobalWrapperSendRequest(t)
	testGlobalWrapperEnableDumps(t)
	DisableInsecureSkipVerify()

	testErr := errors.New("test")
	testDial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, testErr
	}
	testDialTLS := func(ctx context.Context, network, addr string) (net.Conn, error) {
		return nil, testErr
	}

	marshalFunc := func(v interface{}) ([]byte, error) {
		return nil, testErr
	}
	unmarshalFunc := func(data []byte, v interface{}) error {
		return testErr
	}
	u, _ := url.Parse("http://dummy.proxy.local")
	proxy := http.ProxyURL(u)
	form := make(url.Values)
	form.Add("test", "test")

	testGlobalClientSettingWrapper(t,
		SetCookieJar(nil),
		SetDialTLS(testDialTLS),
		SetDial(testDial),
		SetTLSHandshakeTimeout(time.Second),
		EnableAllowGetMethodPayload(),
		DisableAllowGetMethodPayload(),
		SetJsonMarshal(marshalFunc),
		SetJsonUnmarshal(unmarshalFunc),
		SetXmlMarshal(marshalFunc),
		SetXmlUnmarshal(unmarshalFunc),
		EnableTraceAll(),
		DisableTraceAll(),
		OnAfterResponse(func(client *Client, response *Response) error {
			return nil
		}),
		OnBeforeRequest(func(client *Client, request *Request) error {
			return nil
		}),
		SetProxyURL("http://dummy.proxy.local"),
		SetProxyURL("bad url"),
		SetProxy(proxy),
		SetCommonContentType(jsonContentType),
		SetCommonHeader("my-header", "my-value"),
		SetCommonHeaders(map[string]string{
			"header1": "value1",
			"header2": "value2",
		}),
		SetCommonBasicAuth("imroc", "123456"),
		SetCommonBearerAuthToken("123456"),
		SetUserAgent("test"),
		SetTimeout(1*time.Second),
		SetLogger(createDefaultLogger()),
		SetScheme("https"),
		EnableDebugLog(),
		DisableDebugLog(),
		SetCommonCookies(&http.Cookie{Name: "test", Value: "test"}),
		SetCommonQueryString("test1=test1"),
		SetCommonPathParams(map[string]string{"test1": "test1"}),
		SetCommonPathParam("test2", "test2"),
		AddCommonQueryParam("test1", "test11"),
		SetCommonQueryParam("test1", "test111"),
		SetCommonQueryParams(map[string]string{"test1": "test1"}),
		EnableInsecureSkipVerify(),
		DisableInsecureSkipVerify(),
		DisableCompression(),
		EnableCompression(),
		DisableKeepAlives(),
		EnableKeepAlives(),
		SetRootCertsFromFile(tests.GetTestFilePath("sample-root.pem")),
		SetRootCertFromString(string(tests.GetTestFileContent(t, "sample-root.pem"))),
		SetCerts(tls.Certificate{}, tls.Certificate{}),
		SetCertFromFile(
			tests.GetTestFilePath("sample-client.pem"),
			tests.GetTestFilePath("sample-client-key.pem"),
		),
		SetOutputDirectory(testDataPath),
		SetBaseURL("http://dummy-req.local/test"),
		SetCommonFormDataFromValues(form),
		SetCommonFormData(map[string]string{"test2": "test2"}),
		DisableAutoReadResponse(),
		EnableAutoReadResponse(),
		EnableDumpAll(),
		EnableDumpAllAsync(),
		EnableDumpAllWithoutBody(),
		EnableDumpAllWithoutResponse(),
		EnableDumpAllWithoutRequest(),
		EnableDumpAllWithoutHeader(),
		SetLogger(nil),
		EnableDumpAllToFile(filepath.Join(testDataPath, "path-not-exists", "dump.out")),
		EnableDumpAllToFile(tests.GetTestFilePath("tmpdump.out")),
		SetCommonDumpOptions(&DumpOptions{
			RequestHeader: true,
		}),
		DisableDumpAll(),
		SetRedirectPolicy(NoRedirectPolicy()),
		EnableForceHTTP1(),
		EnableForceHTTP2(),
		DisableForceHttpVersion(),
		SetAutoDecodeContentType("json"),
		SetAutoDecodeContentTypeFunc(func(contentType string) bool { return true }),
		SetAutoDecodeAllContentType(),
		DisableAutoDecode(),
		EnableAutoDecode(),
		AddCommonRetryCondition(func(resp *Response, err error) bool { return true }),
		SetCommonRetryCondition(func(resp *Response, err error) bool { return true }),
		AddCommonRetryHook(func(resp *Response, err error) {}),
		SetCommonRetryHook(func(resp *Response, err error) {}),
		SetCommonRetryCount(2),
		SetCommonRetryInterval(func(resp *Response, attempt int) time.Duration {
			return 1 * time.Second
		}),
		SetCommonRetryBackoffInterval(1*time.Millisecond, 2*time.Second),
		SetCommonRetryFixedInterval(1*time.Second),
		SetUnixSocket("/var/run/custom.sock"),
	)
	os.Remove(tests.GetTestFilePath("tmpdump.out"))

	config := GetTLSClientConfig()
	assertEqual(t, config, DefaultClient().t.TLSClientConfig)

	r := R()
	assertEqual(t, true, r != nil)
	c := C()

	c.SetTimeout(10 * time.Second)
	SetDefaultClient(c)
	assertEqual(t, true, DefaultClient().httpClient.Timeout == 10*time.Second)
	assertEqual(t, GetClient(), DefaultClient().httpClient)

	r = NewRequest()
	assertEqual(t, true, r != nil)
	c = NewClient()
	assertEqual(t, true, c != nil)
}

func TestTrailer(t *testing.T) {
	resp, err := tc().EnableForceHTTP1().R().Get("/chunked")
	assertSuccess(t, resp, err)
	_, ok := resp.Trailer["Expires"]
	if !ok {
		t.Error("trailer not exists")
	}
}
