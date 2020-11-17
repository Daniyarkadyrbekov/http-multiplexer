package main

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func init() {

}

func TestSimpleRequest(t *testing.T) {
	jsonUrls := `
[
"http://jsonplaceholder.typicode.com/posts/1",
"http://jsonplaceholder.typicode.com/posts/2",
"http://jsonplaceholder.typicode.com/posts/3"
]
`

	resp, err := http.Post("http://localhost:8080/", "aplication/json", strings.NewReader(jsonUrls))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	var res map[string]string

	d, err := ioutil.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(d, &res))
	expRes := map[string]string{
		"http://jsonplaceholder.typicode.com/posts/1": `{
  "userId": 1,
  "id": 1,
  "title": "sunt aut facere repellat provident occaecati excepturi optio reprehenderit",
  "body": "quia et suscipit\nsuscipit recusandae consequuntur expedita et cum\nreprehenderit molestiae ut ut quas totam\nnostrum rerum est autem sunt rem eveniet architecto"
}`,
		"http://jsonplaceholder.typicode.com/posts/2": `{
  "userId": 1,
  "id": 2,
  "title": "qui est esse",
  "body": "est rerum tempore vitae\nsequi sint nihil reprehenderit dolor beatae ea dolores neque\nfugiat blanditiis voluptate porro vel nihil molestiae ut reiciendis\nqui aperiam non debitis possimus qui neque nisi nulla"
}`,
		"http://jsonplaceholder.typicode.com/posts/3": `{
  "userId": 1,
  "id": 3,
  "title": "ea molestias quasi exercitationem repellat qui ipsa sit aut",
  "body": "et iusto sed quo iure\nvoluptatem occaecati omnis eligendi aut ad\nvoluptatem doloribus vel accusantium quis pariatur\nmolestiae porro eius odio et labore et velit aut"
}`,
	}
	require.Equal(t, expRes, res)
}

func TestIncorrectRequest(t *testing.T) {
	jsonUrls := `
[
"http://jsonplaceholder.typicode.com/posts/1",
"http://jsonplaceholder.typicode.com/posts/2",
"http://jsonplaceholder.typicode.com/posts/3",
"http:/incorrect.qwerty.ytre",
"another-incorrect-url",
]
`

	resp, err := http.Post("http://localhost:8080/", "aplication/json", strings.NewReader(jsonUrls))
	require.NoError(t, err)
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestReadEmbeddedStrings(t *testing.T) {

	const maxUrlsCount = 4
	{
		r := strings.NewReader(`[]`)
		res, err := readEmbeddedStrings(r, maxUrlsCount)
		require.NoError(t, err)
		require.Len(t, res, 0)
	}

	{
		r := strings.NewReader(`["url1", "url2", "ur]`)
		res, err := readEmbeddedStrings(r, maxUrlsCount)
		require.EqualError(t, err, "not completed word exists")
		require.Len(t, res, 0)
	}

	{
		r := strings.NewReader(`["url1", "url2", "url3", "url4", "url5"]`)
		res, err := readEmbeddedStrings(r, maxUrlsCount)
		require.EqualError(t, err, "request max urls count exceeded")
		require.Len(t, res, 0)
	}

	{
		r := strings.NewReader(`["url1", "url2", "url3", "url4"]`)
		res, err := readEmbeddedStrings(r, maxUrlsCount)
		require.NoError(t, err)
		require.Equal(t, []string{"url1", "url2", "url3", "url4"}, res)
	}

}

func TestLimitHandlersConcurrency(t *testing.T) {

	const concurrency = 4
	const sleepTime = 200 * time.Millisecond

	ts := httptest.NewServer(limitedClientsHandler(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(sleepTime)
	}, concurrency))
	defer ts.Close()

	{
		//without concurrency
		start := time.Now()
		for i := 0; i < 4; i++ {
			_, err := http.Get(ts.URL)
			require.NoError(t, err)
		}
		require.GreaterOrEqual(t, time.Since(start).Nanoseconds(), (sleepTime * 4).Nanoseconds())
	}

	{
		//with minimal concurrency
		wg := sync.WaitGroup{}
		start := time.Now()
		for i := 0; i < 4; i++ {
			wg.Add(1)
			go func() {
				_, err := http.Get(ts.URL)
				require.NoError(t, err)
				wg.Done()
			}()
		}
		wg.Wait()
		require.GreaterOrEqual(t, time.Since(start).Nanoseconds(), (sleepTime).Nanoseconds())
		require.LessOrEqual(t, time.Since(start).Nanoseconds(), (sleepTime * 2).Nanoseconds())
	}

	{
		//requests more than concurrency
		wg := sync.WaitGroup{}
		start := time.Now()
		for i := 0; i < 9; i++ {
			wg.Add(1)
			go func() {
				_, err := http.Get(ts.URL)
				require.NoError(t, err)
				wg.Done()
			}()
		}
		wg.Wait()
		require.GreaterOrEqual(t, time.Since(start).Nanoseconds(), (sleepTime * 3).Nanoseconds())
		require.LessOrEqual(t, time.Since(start).Nanoseconds(), (sleepTime * 4).Nanoseconds())
	}
}

func TestCancelRequest(t *testing.T) {
	jsonUrls := `
[
"http://jsonplaceholder.typicode.com/posts/1",
"http://jsonplaceholder.typicode.com/posts/2",
"http://jsonplaceholder.typicode.com/posts/3"
]
`
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost:8080/", strings.NewReader(jsonUrls))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "aplication/json")
	cancel()
	resp, err := http.DefaultClient.Do(req)
	require.EqualError(t, err, "Post \"http://localhost:8080/\": context canceled")
	require.Nil(t, resp)
}
