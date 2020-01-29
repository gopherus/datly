package reader

import (
	"github.com/viant/datly/base/contract"
)

//Response represents
type Response struct {
	contract.Response
}

//NewResponse creates a response
func NewResponse() *Response {
	return &Response{
		Response: *contract.NewResponse(),
	}
}
