package biliutil

import (
	"net/http"
	"net/http/httptest"
)

type recorderDoer func(req *http.Request) *httptest.ResponseRecorder

func (f recorderDoer) Do(req *http.Request) (*http.Response, error) {
	recorder := f(req)
	return recorder.Result(), nil
}

func (f recorderDoer) RoundTrip(req *http.Request) (*http.Response, error) {
	recorder := f(req)
	return recorder.Result(), nil
}

func mockHTTPDoer(handler http.Handler) HTTPDoer {
	return recorderDoer(func(req *http.Request) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	})
}

func mockHTTPClient(handler http.Handler) *http.Client {
	return &http.Client{
		Transport: recorderDoer(func(req *http.Request) *httptest.ResponseRecorder {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			return recorder
		}),
	}
}
