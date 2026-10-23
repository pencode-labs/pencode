package main

import "github.com/diiyw/onix/module"

type SceneWorkspace struct{}

func (m *SceneWorkspace) OnMessage(c *module.Context, message module.Message) error {
	return routePencodeScene(c, message)
}
