package proxy

import (
	"bytes"
	"fmt"
	"net"
	"io/ioutil"
	"net/http"
	"compress/gzip"
	"strings"
)

const (
	LogWhenOnError  = "onError"  // print log only for those who raise exceptions
	LogWhenOnNon200 = "onNon200" // print log for those who have a non-200 response, or those who raise exceptions
	LogWhenAlways   = "always"   // print log for every request

	LogWhatBasic    = "basic"    // print the request's method and URI and the response status code (and the exception message, if exception raised) in the log
	LogWhatDetailed = "detailed" // print the request's method, URI and body, and the response status code and body (and the exception message, if exception raised) in the log
	//LogAll          = "all"      // to be supported later. Compared to whatlog_detail, all Headers are printed in whatlog_all
)

type Params struct {
	Local  string
	Remote string
	Method string

	CertPath string
	Insecure bool

	Whenlog string
	Whatlog string
}

type Provider interface {
	RequestAccess() error
	Client() *http.Client
	Modify(params *Params, req *http.Request)
}

type Proxy struct {
	*Params
	Provider Provider
}

func (p *Proxy) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	var params = p.Params

	//completeFlag indicates if the server has finished constructing the response. It's initialized with value false.
	//It will be set to true when response construction finished.
	completeFlag := false

	// Notice that here the func in defer is needed!
	// By doing so, defer will register the pointer flag and rw, and we can change what the pointers point to later.
	// Without the func, what defer registers is not the pointers, so defer will know nothing about the later changes to completeFlag, and defer cannot write to the origin ResponseWriter.
	defer func(flag *bool, rw *http.ResponseWriter) {
		if !(*flag) {
			(*rw).WriteHeader(502)
		}
	}(&completeFlag, &rw)

	// logFlag is initialized with value true.
	// it will be set false if our program finally ensure it's not needed to print the log (depends on the running state and params.whenlog).
	// when ServeHTTP finished (or crashed), if logFlag remains true, log will be printed
	logFlag := true

	logStrBuilder := new(strings.Builder)

	// Notice that here the func in defer is needed!
	// By doing so, defer will register the pointer strBuilder and flag, and we can change what the pointers point to later.
	// Without the func, what defer registers is not the pointers, and defer will know nothing about the later changes to stringbulider and flag.
	defer func(strBuilder *strings.Builder, flag *bool) {
		if *flag {
			fmt.Println(strBuilder.String())
		}
	}(logStrBuilder, &logFlag)

	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(req.Body)
	
	req.URL.Host = params.Remote
	req.URL.Scheme = "https"

	if isLoopbackAddr(req.URL.Host) {
		req.URL.Scheme = "http"
	}

	logStrBuilder.WriteString(fmt.Sprintln("Requesting:", req.Method, req.URL))
	if params.Whatlog >= LogWhatDetailed {
		logStrBuilder.WriteString(buf.String() + "\n")
	}

	// build the transport request
	req1, err := http.NewRequest(req.Method, req.URL.String(), buf)
	if err != nil {
		logStrBuilder.WriteString(fmt.Sprintln("Error when make transport request:\n", err))
		return
	}
	req1.ContentLength = req.ContentLength
	req1.Header = req.Header
	req1.Method = req.Method

	p.Provider.Modify(params, req1)
	//req1.SetBasicAuth(params.Username, params.Password)

	// do request and get response
	response, err := p.Provider.Client().Do(req1)
	if err != nil {
		logStrBuilder.WriteString(fmt.Sprintln("Error when send the transport request:\n", err))
		return
	}
	defer response.Body.Close()
	buf = new(bytes.Buffer)
	
	// Add GZIP support 
	switch response.Header.Get("Content-Encoding") {
	case "gzip":
		reader, err := gzip.NewReader(response.Body)
		if err != nil {
			logStrBuilder.WriteString(fmt.Sprintln("Error while decoding gzip data:\n", err))
			return 
		}
		defer reader.Close()
		data, err := ioutil.ReadAll(reader)
		if err != nil {
			logStrBuilder.WriteString(fmt.Sprintln("Error while reading gzip data:\n", err))
			return 
		}
		buf.Write(data)
	default:
		_, _ = buf.ReadFrom(response.Body)
	}
	

	logStrBuilder.WriteString(fmt.Sprintln("Response Status Code:", response.StatusCode))

	if params.Whatlog >= LogWhatDetailed {
		logStrBuilder.WriteString(fmt.Sprintln(buf.String()))
	}

	rw.WriteHeader(response.StatusCode)
	_, _ = rw.Write(buf.Bytes())

	//Set completeFlag to indicate that the response construction finished
	completeFlag = true

	// check if logFlag should be set to false
	if params.Whenlog == LogWhenOnError {
		logFlag = false
	}
	if params.Whenlog == LogWhenOnNon200 {
		if response.StatusCode == 200 {
			logFlag = false
		}
	}
}

func isLoopbackAddr(addr string) bool {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	host, _, _ := net.SplitHostPort(addr)
	for _, h := range hosts {
		if h == host {
			return true
		}
	}
	return false
}
