package azure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/stulzq/azure-openai-proxy/util"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

func ProxyWithConverter(requestConverter RequestConverter) gin.HandlerFunc {
	return func(c *gin.Context) {
		Proxy(c, requestConverter)
	}
}

// Proxy Azure OpenAI
func Proxy(c *gin.Context, requestConverter RequestConverter) {
	if c.Request.Method == http.MethodOptions {
		c.Header("Access-Control-Allow-Origin", "*")
		c.Header("Access-Control-Allow-Methods", "GET, OPTIONS, POST")
		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type")
		c.Status(200)
		return
	}

	director := func(req *http.Request) {
		if req.Body == nil {
			util.SendError(c, errors.New("request body is empty"))
			return
		}
		body, _ := io.ReadAll(req.Body)

		var model string

		// 解析 JSON 数据为一个 map 对象
		var data map[string]interface{}
		err := json.Unmarshal(body, &data)
		if err != nil {
			log.Println("解析 JSON 数据失败：", err)
		}

		key := "model"
		model = (data[key]).(string)
		delete(data, key)
		delete(data, "top_p")
		delete(data, "presence_penalty")
		delete(data, "max_tokens")
		// 将修改后的 map 对象重新序列化为 []byte 数据
		modifiedData, err := json.Marshal(data)
		if err != nil {
			log.Println("序列化 JSON 数据失败：", err)
		}

		body = modifiedData
		log.Println(string(modifiedData))

		req.Body = io.NopCloser(bytes.NewBuffer(body))
		req.ContentLength = int64(len(body))

		// get model from url params or body
		if model == "" {
			model = c.Param("model")
			if model == "" {
				_model, err := sonic.Get(body, "model")
				if err != nil {
					util.SendError(c, errors.Wrap(err, "get model error"))
					return
				}
				_modelStr, err := _model.String()
				if err != nil {
					util.SendError(c, errors.Wrap(err, "get model name error"))
					return
				}
				model = _modelStr
			}
		}

		// get deployment from request
		deployment, err := GetDeploymentByModel(model)
		if err != nil {
			util.SendError(c, err)
			return
		}

		// get auth token from header or deployemnt config
		token := deployment.ApiKey
		if token == "" {
			rawToken := req.Header.Get("Authorization")
			token = strings.TrimPrefix(rawToken, "Bearer ")
		}
		if token == "" {
			util.SendError(c, errors.New("token is empty"))
			return
		}
		req.Header.Set(AuthHeaderKey, token)
		req.Header.Del("Authorization")

		originURL := req.URL.String()
		req, err = requestConverter.Convert(req, deployment)
		if err != nil {
			util.SendError(c, errors.Wrap(err, "convert request error"))
			return
		}
		log.Printf("proxying request [%s] %s -> %s", model, originURL, req.URL.String())
	}

	proxy := &httputil.ReverseProxy{Director: director}
	proxy.ServeHTTP(c.Writer, c.Request)

	// issue: https://github.com/Chanzhaoyu/chatgpt-web/issues/831
	if c.Writer.Header().Get("Content-Type") == "text/event-stream" {
		if _, err := c.Writer.Write([]byte{'\n'}); err != nil {
			log.Printf("rewrite response error: %v", err)
		}
	}
}

func GetDeploymentByModel(model string) (*DeploymentConfig, error) {
	deploymentConfig, exist := ModelDeploymentConfig[model]
	if !exist {
		return nil, errors.New(fmt.Sprintf("deployment config for %s not found", model))
	}
	return &deploymentConfig, nil
}
