package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

const (
	MaxConnections           = 100
	MaxEmbeddedUrls          = 20
	MaxConcurrentSubRequests = 4
)

func multiplexer(w http.ResponseWriter, req *http.Request) {

	if req.Method != http.MethodPost {
		http.Error(w, "405 only POST method allowed", http.StatusMethodNotAllowed)
		return
	}

	urls, err := readEmbeddedStrings(req.Body, MaxEmbeddedUrls)
	if err != nil {
		http.Error(w, "500 getting urls from req", http.StatusInternalServerError)
		return
	}

	client := &http.Client{
		Timeout: time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-req.Context().Done()
		cancel()
	}()

	ch := make(chan httpResult, 0)
	pool := make(chan struct{}, MaxConcurrentSubRequests)
	wg := &sync.WaitGroup{}

	res := make(map[string]string, 0)
	for _, url := range urls {
		// In production could use redis or memcached to check already requested urls
		wg.Add(1)
		go getSubRequest(url, client, pool, wg, ch, ctx)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	for result := range ch {

		if result.err != nil {
			cancel()
			http.Error(w, result.err.Error(), http.StatusInternalServerError)
			return
		}

		d, err := ioutil.ReadAll(result.resp.Body)
		if err != nil {
			http.Error(w, "500 subUrl req body reading error", http.StatusInternalServerError)
			return
		}
		result.resp.Body.Close()

		res[result.url] = string(d)
	}

	resBytes, err := json.Marshal(res)
	if err != nil {
		http.Error(w, "500 json result marshaling err", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Write(resBytes)
}

func limitedClientsHandler(f http.HandlerFunc, max int) http.HandlerFunc {
	pool := make(chan struct{}, max)

	return func(w http.ResponseWriter, req *http.Request) {
		pool <- struct{}{}
		defer func() { <-pool }()
		f(w, req)
	}
}

func main() {

	ctx, cancel := context.WithCancel(context.Background())

	httpServer := &http.Server{
		Addr:        ":8080",
		Handler:     limitedClientsHandler(multiplexer, MaxConnections),
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	go func() {
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server ListenAndServe: %v", err)
		}
	}()

	signalChan := make(chan os.Signal, 1)

	signal.Notify(
		signalChan,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGQUIT,
	)

	<-signalChan
	log.Print("os.Interrupt - shutting down...\n")

	go func() {
		<-signalChan
		log.Fatal("os.Kill - terminating...\n")
	}()

	gracefulCtx, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()

	if err := httpServer.Shutdown(gracefulCtx); err != nil {
		log.Printf("shutdown error: %v\n", err)
		os.Exit(1)
	} else {
		log.Printf("gracefully stopped\n")
	}

	cancel()

	os.Exit(0)
}

func readEmbeddedStrings(r io.Reader, max int) (res []string, err error) {

	bufReader := bufio.NewReader(r)
	var b byte
	var inWord bool
	var word []byte
	for {
		b, err = bufReader.ReadByte()
		if err == io.EOF {
			if len(word) != 0 {
				err = errors.New("not completed word exists")
				res = res[:0]
			} else {
				err = nil
			}
			return
		} else if err != nil {
			return
		}

		// https://www.w3schools.com/js/js_json_syntax.asp
		// In JSON, string values must be written with double quotes
		if b == '"' {
			inWord = !inWord
			if !inWord {
				if len(res) >= max {
					res = res[:0]
					err = errors.New("request max urls count exceeded")
					return
				}
				res = append(res, string(word))
				word = word[:0]
			}
			continue
		} else if inWord {
			word = append(word, b)
		}
	}
}

type httpResult struct {
	resp *http.Response
	url  string
	err  error
}

func getSubRequest(lUrl string, client *http.Client, pool chan struct{}, wg *sync.WaitGroup, ch chan httpResult, ctx context.Context) {
	pool <- struct{}{}
	defer wg.Done()
	defer func() { <-pool }()

	select {
	case <-ctx.Done():
		return
	default:
	}

	var err error
	var resp *http.Response
	defer func() {
		ch <- httpResult{
			resp: resp,
			url:  lUrl,
			err:  err,
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lUrl, nil)
	if err != nil {
		log.Printf("creating subReq err = %s\n", err.Error())
		return
	}
	resp, err = client.Do(req)
	if err != nil {
		log.Printf("subReq err = %s\n", err.Error())
	}
}
