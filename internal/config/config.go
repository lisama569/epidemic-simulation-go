// Package config 负责情景配置的 JSON 解析、旧版本归一与校验。
//
// 版本约定：
//   - configVersion 缺省视为 1（历史配置），按确定规则归一到版本 2；
//   - 版本 2 使用本文件中 model.Scenario 的字段名（驼峰）；
//   - 其它版本一律以 ErrUnsupportedVersion 拒绝，不静默套用默认值；
//   - 未知字段一律拒绝（严格解析），避免拼写错误被静默忽略。
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lisama569/epidemic-simulation-go/internal/model"
)

// CurrentConfigVersion 是当前配置版本。
const CurrentConfigVersion = 2

// v1Group 是旧版本（v1）分组配置的字段布局。
type v1Group struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Susceptible float64 `json:"susceptible"`
	Infected    float64 `json:"infected"`
	Recovered   float64 `json:"recovered"`
	Exposed     float64 `json:"exposed"` // 仅 paradigm=SEIR 合法
}

// v1Intervention 是旧版本干预布局。
type v1Intervention struct {
	Kind        string   `json:"kind"` // "lockdown" | "quarantine"
	Targets     []string `json:"targets"`
	StartTime   float64  `json:"startTime"` // 单位：天
	EndTime     float64  `json:"endTime"`
	Efficacy    *float64 `json:"efficacy"`  // lockdown 缩减比例
	Adherence   *float64 `json:"adherence"` // 缺省不允许静默默认，归一阶段按规则取 1.0
	Rate        *float64 `json:"rate"`      // quarantine 每日速率
	MaxIsolated *float64 `json:"maxIsolated"`
}

// v1Scenario 是旧版本整体布局（蛇形字段名）。
type v1Scenario struct {
	ConfigVersion *int             `json:"configVersion"`
	Paradigm      string           `json:"paradigm"` // "SIR" | "SEIR"
	Disease       v1Disease        `json:"disease"`
	TimeHorizon   float64          `json:"timeHorizon"`
	TimeStep      float64          `json:"timeStep"`
	Population    []v1Group        `json:"population"`
	Contacts      [][]float64      `json:"contacts"`
	Migration     [][]float64      `json:"migration"`
	Measures      []v1Intervention `json:"measures"`
	Seed          int64            `json:"seed"`
}

type v1Disease struct {
	Transmission float64 `json:"transmission"`
	Recovery     float64 `json:"recovery"`
	Incubation   float64 `json:"incubation"` // 仅 SEIR
}

// Load 解析 JSON 配置并按确定规则归一旧版本；非法输入返回带 model.ErrorKind 的错误。
func Load(data []byte) (*model.Scenario, error) {
	// 先只取版本号（容忍未知字段），决定走哪条解析路径。
	var probe struct {
		ConfigVersion *int `json:"configVersion"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, model.NewError(model.ErrConfigSyntax, "JSON 语法错误: %v", err)
	}
	version := 1
	if probe.ConfigVersion != nil {
		version = *probe.ConfigVersion
	}
	switch version {
	case 1:
		return loadV1(data)
	case 2:
		return loadV2(data)
	default:
		return nil, model.NewError(model.ErrUnsupportedVersion,
			"configVersion=%d 不受支持，当前支持版本为 1(将自动归一) 与 2", version)
	}
}

func loadV1(data []byte) (*model.Scenario, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var old v1Scenario
	if err := dec.Decode(&old); err != nil {
		return nil, model.NewError(model.ErrConfigSyntax, "v1 配置字段非法: %v", err)
	}
	sc := &model.Scenario{ConfigVersion: 2}
	switch strings.ToUpper(old.Paradigm) {
	case "SIR":
		sc.Paradigm = model.ParadigmSIR
	case "SEIR":
		sc.Paradigm = model.ParadigmSEIR
	default:
		return nil, model.NewError(model.ErrConfigSyntax, "v1 paradigm 必须为 SIR 或 SEIR, 实际=%q", old.Paradigm)
	}
	sc.Parameters = model.Parameters{
		Beta:  old.Disease.Transmission,
		Gamma: old.Disease.Recovery,
		Sigma: old.Disease.Incubation,
	}
	sc.HorizonDays = old.TimeHorizon
	sc.StepDays = old.TimeStep
	sc.ContactMatrix = old.Contacts
	sc.MigrationMatrix = old.Migration
	sc.RandomSeed = old.Seed
	for _, g := range old.Population {
		sc.Groups = append(sc.Groups, model.Group{
			ID: g.ID, Name: g.Name,
			S0: g.Susceptible, E0: g.Exposed, I0: g.Infected, R0: g.Recovered,
		})
	}
	for i, m := range old.Measures {
		iv, err := normalizeV1Measure(m)
		if err != nil {
			return nil, model.NewError(model.ErrConfigSyntax, "measures[%d]: %v", i, err)
		}
		sc.Interventions = append(sc.Interventions, iv)
	}
	if err := Normalize(sc); err != nil {
		return nil, err
	}
	return sc, nil
}

// normalizeV1Measure 把旧版干预映射到新类型。
// 确定归一规则：
//   - kind "lockdown" -> contact_reduction，字段 efficacy->strength；
//   - kind "quarantine" -> isolation，字段 rate->ratePerDay，maxIsolated->capacity；
//   - adherence 缺省时按确定规则取 1.0（完全依从），这是唯一允许的缺省归一，
//     因为旧版本里该字段对所有干预语义一致；其余数值字段缺省即拒绝。
func normalizeV1Measure(m v1Intervention) (model.Intervention, error) {
	iv := model.Intervention{
		Groups:    m.Targets,
		StartDays: m.StartTime,
		EndDays:   m.EndTime,
	}
	if m.Adherence != nil {
		iv.Compliance = *m.Adherence
	} else {
		iv.Compliance = 1.0
	}
	switch m.Kind {
	case "lockdown":
		iv.Type = model.InterventionContactReduction
		if m.Efficacy == nil {
			return iv, fmt.Errorf("lockdown 缺少 efficacy")
		}
		iv.Strength = *m.Efficacy
		if m.Rate != nil || m.MaxIsolated != nil {
			return iv, fmt.Errorf("lockdown 不得携带 rate/maxIsolated 字段")
		}
	case "quarantine":
		iv.Type = model.InterventionIsolation
		if m.Rate == nil {
			return iv, fmt.Errorf("quarantine 缺少 rate")
		}
		iv.RatePerDay = *m.Rate
		iv.Capacity = m.MaxIsolated
		if m.Efficacy != nil {
			return iv, fmt.Errorf("quarantine 不得携带 efficacy 字段")
		}
	default:
		return iv, fmt.Errorf("未知 kind=%q（支持 lockdown/quarantine）", m.Kind)
	}
	return iv, nil
}

func loadV2(data []byte) (*model.Scenario, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var sc model.Scenario
	if err := dec.Decode(&sc); err != nil {
		return nil, model.NewError(model.ErrConfigSyntax, "v2 配置字段非法: %v", err)
	}
	sc.ConfigVersion = 2
	if err := Normalize(&sc); err != nil {
		return nil, err
	}
	return &sc, nil
}

// Normalize 对已解析配置做归一后检查：版本必须为当前版本。
// 真正的语义自洽性校验在 engine.Validate 完成。
func Normalize(sc *model.Scenario) error {
	if sc == nil {
		return model.NewError(model.ErrConfigSyntax, "空配置")
	}
	if sc.ConfigVersion != CurrentConfigVersion {
		return model.NewError(model.ErrUnsupportedVersion,
			"归一后版本应为 %d，实际为 %d", CurrentConfigVersion, sc.ConfigVersion)
	}
	return nil
}
