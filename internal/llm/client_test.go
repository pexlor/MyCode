package llm

import (
	"fmt"
)

func main() {
	client, _ := NewClient(&ModelParm{
		Protocol:  "openai-compat",
		BaseURL:   "https://llm-lgsv9uhdbprfr0vv.cn-beijing.maas.aliyuncs.com/compatible-mode/v1",
		APIKey:    "sk-72683ab6f2174c81bc7d05d13b4c7296",
		ModelName: "qwen-plus",
	})
	eventChan, errChan := client.Stream(&StreamRequest{})
	select {
	case event := <-eventChan:
		switch e := event.(type) {
		case StreamEnd:
			fmt.Println("end")
			return
		case TextStream:
			fmt.Println(e.Text)
		}
	case <-errChan:
	}
}
