package llm

import (
	"fmt"
)

func main() {
	client, _ := NewClient(&ModelParm{
		Protocol:  "openai-compat",
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
