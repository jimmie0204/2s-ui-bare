package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shenaba/2s-ui/database"
	"github.com/shenaba/2s-ui/database/model"

	"gorm.io/gorm"
)

const maxBareConfigSize = 32 << 20

func MaxBareConfigSize() int64 { return maxBareConfigSize }

// ImportBareSingBoxConfig 将现有裸 sing-box 配置导入为中心面板的节点副本。
// 只导入入站和用户，不覆盖中心面板自身的出站、DNS、路由和证书表。
func ImportBareSingBoxConfig(nodeID uint, raw []byte, actor string) (map[string]any, error) {
	if len(raw) == 0 || len(raw) > maxBareConfigSize {
		return nil, errors.New("sing-box 配置为空或超过 32 MiB")
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return nil, fmt.Errorf("sing-box 配置不是有效 JSON: %w", err)
	}
	items, ok := root["inbounds"].([]any)
	if !ok || len(items) == 0 {
		return nil, errors.New("配置中没有 inbounds")
	}

	db := database.GetDB()
	var importedInbounds, importedClients int
	err := db.Transaction(func(tx *gorm.DB) error {
		var node model.Node
		if err := tx.First(&node, nodeID).Error; err != nil {
			return fmt.Errorf("节点不存在: %w", err)
		}

		clientIDs := map[string]uint{}
		clientModels := map[string]*model.Client{}
		for index, value := range items {
			inbound, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("inbounds[%d] 不是对象", index)
			}
			typ, _ := inbound["type"].(string)
			tag, _ := inbound["tag"].(string)
			if strings.TrimSpace(typ) == "" || strings.TrimSpace(tag) == "" {
				return fmt.Errorf("inbounds[%d] 缺少 type 或 tag", index)
			}
			var count int64
			if err := tx.Model(&model.Inbound{}).Where("tag = ?", tag).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				return fmt.Errorf("入站 tag %q 已存在，请先改名或删除重复入站", tag)
			}

			users, _ := inbound["users"].([]any)
			options := cloneJSONMap(inbound)
			delete(options, "id")
			delete(options, "tag")
			delete(options, "type")
			delete(options, "users")
			optionsJSON, err := json.Marshal(options)
			if err != nil {
				return err
			}
			inboundModel := &model.Inbound{
				Type: typ, Tag: tag, NodeId: &nodeID, Enable: true,
				Options: optionsJSON, Addrs: json.RawMessage(`[]`), OutJson: json.RawMessage(`{}`),
			}
			if err := tx.Create(inboundModel).Error; err != nil {
				return err
			}
			importedInbounds++

			for _, value := range users {
				user, ok := value.(map[string]any)
				if !ok {
					continue
				}
				name, _ := user["name"].(string)
				name = strings.TrimSpace(name)
				if name == "" {
					continue
				}
				client := clientModels[name]
				if client == nil {
					var existing model.Client
					result := tx.Where("name = ?", name).First(&existing)
					if result.Error == nil {
						client = &existing
						clientModels[name] = client
					} else if !errors.Is(result.Error, gorm.ErrRecordNotFound) {
						return result.Error
					} else {
						client = &model.Client{Name: name, Enable: true, Group: "@cluster", Config: json.RawMessage(`{}`), Inbounds: json.RawMessage(`[]`), Links: json.RawMessage(`[]`)}
						clientModels[name] = client
					}
				}
				config := map[string]any{}
				if len(client.Config) > 0 {
					_ = json.Unmarshal(client.Config, &config)
				}
				userCopy := cloneJSONMap(user)
				userCopy["name"] = name
				config[typ] = userCopy
				client.Config, _ = json.Marshal(config)
				inboundIDs := []uint{}
				if len(client.Inbounds) > 0 {
					_ = json.Unmarshal(client.Inbounds, &inboundIDs)
				}
				if !containsUint(inboundIDs, inboundModel.Id) {
					inboundIDs = append(inboundIDs, inboundModel.Id)
				}
				client.Inbounds, _ = json.Marshal(inboundIDs)
				client.Group = "@cluster"
				if client.Id == 0 {
					if err := tx.Create(client).Error; err != nil {
						return err
					}
					importedClients++
				} else if err := tx.Save(client).Error; err != nil {
					return err
				}
				clientIDs[name] = client.Id
			}
		}
		if err := tx.Model(&model.Node{}).Where("id = ?", nodeID).Updates(map[string]any{"dirty": true}).Error; err != nil {
			return err
		}
		obj, _ := json.Marshal(map[string]any{"nodeId": nodeID, "inbounds": importedInbounds, "clients": importedClients})
		return tx.Create(&model.Changes{DateTime: time.Now().Unix(), Actor: actor, Key: "bare-config", Action: "import", Obj: obj}).Error
	})
	if err != nil {
		return nil, err
	}
	SetLastUpdate(time.Now().Unix())
	return map[string]any{"nodeId": nodeID, "inbounds": importedInbounds, "clients": importedClients}, nil
}

func cloneJSONMap(input map[string]any) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func containsUint(values []uint, target uint) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
