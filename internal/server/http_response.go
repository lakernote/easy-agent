package server

import (
	"encoding/json"
	"io"
	"net/http"
)

func decodeJSON(response http.ResponseWriter, request *http.Request, value any) bool {
	// 附件使用 JSON Base64 传输；10 MiB 原始数据编码后约为 13.4 MiB。
	request.Body = http.MaxBytesReader(response, request.Body, 16*1024*1024)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(response, http.StatusBadRequest, "请求格式不正确: "+err.Error())
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			writeError(response, http.StatusBadRequest, "请求只能包含一个 JSON 对象")
		} else {
			writeError(response, http.StatusBadRequest, "请求格式不正确: "+err.Error())
		}
		return false
	}
	return true
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, status int, message string) {
	writeJSON(response, status, map[string]string{"error": message})
}
