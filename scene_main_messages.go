package main

import (
	"github.com/diiyw/onix/module"
	"github.com/diiyw/onix/project"
)

type SceneMain struct{}

func (m *SceneMain) OnMessage(c *module.Context, message module.Message) error {
	return routePencodeScene(c, message)
}
func routePencodeScene(c *module.Context, message module.Message) error {
	if message.Topic == "pencode.navigate" && message.From == "workbench" {
		workspace, ok := module.Value[bool](message)
		if !ok {
			return module.ErrType
		}
		id := project.SceneID("main.json")
		if workspace {
			id = "workspace.json"
		}
		return c.Switch(module.SceneSwitchRequest{Scene: id, Retain: true})
	}
	if message.Topic == "scene.switch.result" {
		result, _ := module.Value[module.SceneSwitchResult](message)
		if result.Err != nil {
			return module.Send(c, "workbench", "pencode.error", result.Err.Error())
		}
	}
	if message.Topic == "openProject" {
		return c.Forward("workbench", message)
	}
	if message.Topic == module.InputTopic && message.From != "" {
		return c.Forward(message.From, message)
	}
	return nil
}
