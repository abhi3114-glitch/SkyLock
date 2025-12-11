package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	// Global flags
	serverAddr := flag.String("server", "http://localhost:8080", "SkyLock server address")
	flag.Parse()

	if flag.NArg() < 1 {
		printUsage()
		os.Exit(1)
	}

	cmd := flag.Arg(0)
	args := flag.Args()[1:]

	client := &Client{baseURL: *serverAddr}

	var err error
	switch cmd {
	case "leader":
		err = client.getLeader()
	case "status":
		err = client.getStatus()
	case "session":
		if len(args) < 1 {
			fmt.Println("Usage: skylockctl session <create|heartbeat|close> [args]")
			os.Exit(1)
		}
		switch args[0] {
		case "create":
			if len(args) < 2 {
				fmt.Println("Usage: skylockctl session create <client-id>")
				os.Exit(1)
			}
			err = client.createSession(args[1])
		case "heartbeat":
			if len(args) < 2 {
				fmt.Println("Usage: skylockctl session heartbeat <session-id>")
				os.Exit(1)
			}
			err = client.heartbeat(args[1])
		case "close":
			if len(args) < 2 {
				fmt.Println("Usage: skylockctl session close <session-id>")
				os.Exit(1)
			}
			err = client.closeSession(args[1])
		default:
			fmt.Printf("Unknown session command: %s\n", args[0])
			os.Exit(1)
		}
	case "get":
		if len(args) < 1 {
			fmt.Println("Usage: skylockctl get <path>")
			os.Exit(1)
		}
		err = client.getNode(args[0])
	case "create":
		if len(args) < 1 {
			fmt.Println("Usage: skylockctl create <path> [data]")
			os.Exit(1)
		}
		data := ""
		if len(args) > 1 {
			data = args[1]
		}
		err = client.createNode(args[0], data, false, "")
	case "set":
		if len(args) < 2 {
			fmt.Println("Usage: skylockctl set <path> <data>")
			os.Exit(1)
		}
		err = client.setNode(args[0], args[1])
	case "delete":
		if len(args) < 1 {
			fmt.Println("Usage: skylockctl delete <path>")
			os.Exit(1)
		}
		err = client.deleteNode(args[0])
	case "children":
		if len(args) < 1 {
			fmt.Println("Usage: skylockctl children <path>")
			os.Exit(1)
		}
		err = client.getChildren(args[0])
	case "lock":
		if len(args) < 1 {
			fmt.Println("Usage: skylockctl lock <name> <session-id> [owner]")
			os.Exit(1)
		}
		if len(args) < 2 {
			fmt.Println("Usage: skylockctl lock <name> <session-id> [owner]")
			os.Exit(1)
		}
		owner := "cli"
		if len(args) > 2 {
			owner = args[2]
		}
		err = client.acquireLock(args[0], args[1], owner)
	case "unlock":
		if len(args) < 2 {
			fmt.Println("Usage: skylockctl unlock <name> <lock-id>")
			os.Exit(1)
		}
		err = client.releaseLock(args[0], args[1])
	case "lockinfo":
		if len(args) < 1 {
			fmt.Println("Usage: skylockctl lockinfo <name>")
			os.Exit(1)
		}
		err = client.getLockInfo(args[0])
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		printUsage()
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`SkyLock CLI - Distributed Lock Service Client

Usage: skylockctl [flags] <command> [args]

Global Flags:
  -server string    SkyLock server address (default "http://localhost:8080")

Commands:
  leader                           Get current leader
  status                           Get cluster status
  
  session create <client-id>       Create a new session
  session heartbeat <session-id>   Send heartbeat
  session close <session-id>       Close a session
  
  get <path>                       Get node data
  create <path> [data]             Create a node
  set <path> <data>                Set node data
  delete <path>                    Delete a node
  children <path>                  Get children of a node
  
  lock <name> <session-id> [owner] Acquire a lock
  unlock <name> <lock-id>          Release a lock
  lockinfo <name>                  Get lock information`)
}

type Client struct {
	baseURL string
	http    http.Client
}

func (c *Client) doRequest(method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	c.http.Timeout = 30 * time.Second
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

func (c *Client) getLeader() error {
	resp, err := c.doRequest("GET", "/v1/cluster/leader", nil)
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func (c *Client) getStatus() error {
	resp, err := c.doRequest("GET", "/v1/cluster/status", nil)
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func (c *Client) createSession(clientID string) error {
	resp, err := c.doRequest("POST", "/v1/session", map[string]interface{}{
		"client_id":   clientID,
		"ttl_seconds": 30,
	})
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func (c *Client) heartbeat(sessionID string) error {
	resp, err := c.doRequest("POST", "/v1/session/"+sessionID+"/heartbeat", nil)
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func (c *Client) closeSession(sessionID string) error {
	_, err := c.doRequest("DELETE", "/v1/session/"+sessionID, nil)
	if err != nil {
		return err
	}
	fmt.Println("Session closed")
	return nil
}

func (c *Client) getNode(path string) error {
	resp, err := c.doRequest("GET", "/v1/nodes/"+path, nil)
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func (c *Client) createNode(path, data string, ephemeral bool, sessionID string) error {
	resp, err := c.doRequest("PUT", "/v1/nodes/"+path, map[string]interface{}{
		"data":       data,
		"ephemeral":  ephemeral,
		"session_id": sessionID,
	})
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func (c *Client) setNode(path, data string) error {
	resp, err := c.doRequest("POST", "/v1/nodes/"+path, map[string]interface{}{
		"data":    data,
		"version": -1,
	})
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func (c *Client) deleteNode(path string) error {
	_, err := c.doRequest("DELETE", "/v1/nodes/"+path, nil)
	if err != nil {
		return err
	}
	fmt.Println("Node deleted")
	return nil
}

func (c *Client) getChildren(path string) error {
	resp, err := c.doRequest("GET", "/v1/children/"+path, nil)
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func (c *Client) acquireLock(name, sessionID, owner string) error {
	resp, err := c.doRequest("POST", "/v1/locks/"+name, map[string]interface{}{
		"owner":      owner,
		"session_id": sessionID,
		"timeout_ms": 30000,
	})
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}

func (c *Client) releaseLock(name, lockID string) error {
	_, err := c.doRequest("DELETE", "/v1/locks/"+name, map[string]interface{}{
		"lock_id": lockID,
	})
	if err != nil {
		return err
	}
	fmt.Println("Lock released")
	return nil
}

func (c *Client) getLockInfo(name string) error {
	resp, err := c.doRequest("GET", "/v1/locks/"+name, nil)
	if err != nil {
		return err
	}
	fmt.Println(string(resp))
	return nil
}
