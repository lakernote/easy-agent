package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"strconv"
	"strings"

	"github.com/lakernote/easy-agent/internal/agent"
)

// calculateTool 使用 Go 标准库解析表达式，不依赖 Python、Node、bc 等外部运行时。
// 它适合普通确定性算术；矩阵、统计和项目脚本等复杂任务仍交给 shell。
func calculateTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "calculate",
			Description: "计算数学表达式。支持 +、-、*、/、%、括号、pi、e，以及 sqrt、abs、pow、min、max、round、floor、ceil、log、log10、sin、cos、tan。",
			Parameters:  objectSchema(map[string]any{"expression": stringSchema("必填，例如 (12.5*8+20)/4 或 sqrt(2)")}, []string{"expression"}),
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var arguments struct {
				Expression string `json:"expression"`
			}
			if err := json.Unmarshal(raw, &arguments); err != nil {
				return "", fmt.Errorf("计算参数错误: %w", err)
			}
			expression := strings.TrimSpace(arguments.Expression)
			if expression == "" {
				return "", errors.New("expression 不能为空")
			}
			// 接受用户和模型常用的排版运算符，再转换成 Go 表达式解析器认识的字符。
			expression = strings.NewReplacer("×", "*", "÷", "/", "−", "-", "＋", "+").Replace(expression)
			if len(expression) > 4096 {
				return "", errors.New("expression 最长为 4096 个字符")
			}
			node, err := parser.ParseExpr(expression)
			if err != nil {
				return "", fmt.Errorf("无法解析数学表达式: %w", err)
			}
			value, err := evaluate(node)
			if err != nil {
				return "", err
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				return "", errors.New("计算结果不是有限数字")
			}
			encoded, err := json.MarshalIndent(map[string]string{
				"expression": expression,
				"result":     strconv.FormatFloat(value, 'g', -1, 64),
			}, "", "  ")
			return string(encoded), err
		},
	}
}

func evaluate(node ast.Expr) (float64, error) {
	switch value := node.(type) {
	case *ast.BasicLit:
		if value.Kind != token.INT && value.Kind != token.FLOAT {
			return 0, fmt.Errorf("不支持的数字: %s", value.Value)
		}
		parsed, err := strconv.ParseFloat(value.Value, 64)
		if err != nil {
			return 0, fmt.Errorf("数字格式错误: %w", err)
		}
		return parsed, nil
	case *ast.ParenExpr:
		return evaluate(value.X)
	case *ast.UnaryExpr:
		operand, err := evaluate(value.X)
		if err != nil {
			return 0, err
		}
		switch value.Op {
		case token.ADD:
			return operand, nil
		case token.SUB:
			return -operand, nil
		default:
			return 0, fmt.Errorf("不支持的一元运算符: %s", value.Op)
		}
	case *ast.BinaryExpr:
		left, err := evaluate(value.X)
		if err != nil {
			return 0, err
		}
		right, err := evaluate(value.Y)
		if err != nil {
			return 0, err
		}
		switch value.Op {
		case token.ADD:
			return left + right, nil
		case token.SUB:
			return left - right, nil
		case token.MUL:
			return left * right, nil
		case token.QUO:
			if right == 0 {
				return 0, errors.New("不能除以零")
			}
			return left / right, nil
		case token.REM:
			if right == 0 {
				return 0, errors.New("不能对零取余")
			}
			return math.Mod(left, right), nil
		default:
			return 0, fmt.Errorf("不支持的运算符: %s", value.Op)
		}
	case *ast.Ident:
		switch strings.ToLower(value.Name) {
		case "pi":
			return math.Pi, nil
		case "e":
			return math.E, nil
		default:
			return 0, fmt.Errorf("未知常量: %s", value.Name)
		}
	case *ast.CallExpr:
		name, ok := value.Fun.(*ast.Ident)
		if !ok {
			return 0, errors.New("只支持直接调用数学函数")
		}
		arguments := make([]float64, 0, len(value.Args))
		for _, argument := range value.Args {
			result, err := evaluate(argument)
			if err != nil {
				return 0, err
			}
			arguments = append(arguments, result)
		}
		return callMath(strings.ToLower(name.Name), arguments)
	default:
		return 0, fmt.Errorf("不支持的表达式类型: %T", node)
	}
}

func callMath(name string, values []float64) (float64, error) {
	one := map[string]func(float64) float64{
		"sqrt": math.Sqrt, "abs": math.Abs, "round": math.Round, "floor": math.Floor,
		"ceil": math.Ceil, "log": math.Log, "log10": math.Log10,
		"sin": math.Sin, "cos": math.Cos, "tan": math.Tan,
	}
	if function, ok := one[name]; ok {
		if len(values) != 1 {
			return 0, fmt.Errorf("%s 需要 1 个参数", name)
		}
		return function(values[0]), nil
	}
	if len(values) != 2 {
		return 0, fmt.Errorf("%s 需要 2 个参数", name)
	}
	switch name {
	case "pow":
		return math.Pow(values[0], values[1]), nil
	case "min":
		return math.Min(values[0], values[1]), nil
	case "max":
		return math.Max(values[0], values[1]), nil
	default:
		return 0, fmt.Errorf("未知数学函数: %s", name)
	}
}
