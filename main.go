package main

import (
	// "fmt"
	"github.com/garyburd/redigo/redis"
	"github.com/namsral/flag"
	"log"
	// "sync"
	"bytes"
	"encoding/json"
	"github.com/ashwanthkumar/slack-go-webhook"
	// "github.com/lucaslsl/goexeccmd/slack"
	"net"
	"os/exec"
	"strings"
	"time"
)

var (
	redisAddr       = flag.String("redis_address", "localhost:6379", "Redis Address")
	redisChannel    = flag.String("redis_channel", "cmds_tasks", "Tasks Channel")
	slackWebhookURL = flag.String("slack_webhook_url", "", "Slack Webhook URL")
	serverID        = flag.String("server_id", getOutboundIP(), "Server ID")
	serverRole      = flag.String("server_role", "app", "Server Role")
	logMsgs         = flag.Bool("logs", true, "Show Logs")
	pubSubConn      *redis.PubSubConn
)

type TaskInstruction struct {
	Command             string `json:"command"`
	StopPipelineOnError bool   `json:"stop_pipeline_on_error"`
}

type Task struct {
	Name         string            `json:"name"`
	Instructions []TaskInstruction `json:"instructions"`
	ServersIDs   []string          `json:"servers_ids"`
	ServersRoles []string          `json:"servers_roles"`
}

func getOutboundIP() string {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	localAddr := conn.LocalAddr().String()
	idx := strings.LastIndex(localAddr, ":")

	return localAddr[0:idx]
}

func serverIDAllowed(t Task) bool {
	if len(t.ServersIDs) == 0 {
		return true
	}
	for _, sID := range t.ServersIDs {
		if sID == *serverID {
			return true
		}
	}
	return false
}

func serverRoleAllowed(t Task) bool {
	if len(t.ServersRoles) == 0 {
		return true
	}
	for _, role := range t.ServersRoles {
		if strings.ToLower(role) == strings.ToLower(*serverRole) {
			return true
		}
	}
	return false
}

func runOnThisServer(t Task) bool {
	return serverIDAllowed(t) && serverRoleAllowed(t)
}

func reconnectAndSubscribe() {
	for {
		newConn, err := redis.Dial("tcp", *redisAddr)
		if err == nil {
			go sendSlackNotification("redis connection reestablished! waiting for tasks in channel *" + *redisChannel + "*")
			pubSubConn = &redis.PubSubConn{Conn: newConn}
			pubSubConn.Subscribe(*redisChannel)
			return
		}
	}
}

func sendSlackNotification(msg string) {
	formattedMsg := "*[" + *serverID + "][" + strings.ToUpper(*serverRole) + "]* " + msg
	if *logMsgs {
		log.Println(msg)
	}
	if len(*slackWebhookURL) > 0 {
		payload := slack.Payload{Text: formattedMsg}
		time.Sleep(time.Second * 1)
		_ = slack.Send(*slackWebhookURL, "", payload)
	}
}

func executeTask(t Task) {

	if runOnThisServer(t) {
		go sendSlackNotification("task *" + t.Name + "* started")
		for _, i := range t.Instructions {
			cmd := exec.Command("sh", "-c", i.Command)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			cmd.Stdout = &stdout
			cmd.Stderr = &stderr
			err := cmd.Run()
			if err != nil && i.StopPipelineOnError {
				go sendSlackNotification("task *" + t.Name + "* failed ```" + stderr.String() + "```")
				return
			}
		}
		go sendSlackNotification("task *" + t.Name + "* finished")
	}

}

func waitForTasks() {
	for {
		switch v := pubSubConn.Receive().(type) {
		case redis.Message:
			var newTask Task
			err := json.Unmarshal(v.Data, &newTask)
			if err == nil {
				executeTask(newTask)
			}

		case error:
			go sendSlackNotification("redis connection failed!")
			reconnectAndSubscribe()
		}
	}
}

func init() {
	flag.Parse()
}

func main() {
	redisConn, err := redis.Dial("tcp", *redisAddr)
	if err != nil {
		panic(err)
	}
	defer redisConn.Close()
	pubSubConn = &redis.PubSubConn{Conn: redisConn}
	pubSubConn.Subscribe(*redisChannel)
	go sendSlackNotification("redis connection established! waiting for tasks in channel *" + *redisChannel + "*")
	defer pubSubConn.Close()

	waitForTasks()

}
