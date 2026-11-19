package resource

import (
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// 后台前端（web/admin）的脚手架：每个字段在列表、详情、表单、筛选里的写法。和后端一样，模板只做
// 拼装，写法都在这里算好。只组装公共组件（src/components、src/lib），不直接用第三方组件库。

// AdminDir 是前端的根目录（相对仓库根目录）。
const AdminDir = "web/admin/"

// Feature 是资源的前端目录名（同 tag），Route 是路由路径。
func (m *Model) Feature() string { return m.N.Tag }
func (m *Model) Route() string   { return "/" + m.N.Tag }

// PageComponent、DetailComponent 是列表页、详情页的组件名。
func (m *Model) PageComponent() string   { return pascal(m.N.Plural) + "Page" }
func (m *Model) DetailComponent() string { return m.N.Type + "DetailPage" }

// MetaVar 是 meta.gen.ts 里 schema 的变量名（同 tools/gen/meta 的命名：schema 名首字母小写加 Meta）。
func MetaVar(schema string) string { return camel0(schema) + "Meta" }

func camel0(s string) string { return strings.ToLower(s[:1]) + s[1:] }

func (m *Model) EntityMeta() string { return MetaVar(m.N.Type) }
func (m *Model) ListMeta() string   { return m.N.OpList + "Meta" }
func (m *Model) CreateMeta() string { return MetaVar("Create" + m.N.Type + "Request") }
func (m *Model) UpdateMeta() string { return MetaVar("Update" + m.N.Type + "Request") }
func (m *Model) InputMeta(a *ActionM) string {
	return MetaVar(pascal(a.Name) + m.N.Type + "Request")
}

// FiltersType 是列表筛选条件（URL search）的类型名。
func (m *Model) FiltersType() string { return m.N.Type + "Filters" }

// Hook 是写操作的 mutation hook 名。
func (m *Model) Hook(op string) string { return "use" + pascal(op) + m.N.Type }

// ActionHook 是动作的 mutation hook 名，如 usePublishWidget。
func (m *Model) ActionHook(a *ActionM) string { return "use" + pascal(a.Op) }

// ActionDialog 是带参数的动作的对话框组件名。
func (m *Model) ActionDialog(a *ActionM) string { return pascal(a.Op) + "Dialog" }

// APIPath 是 openapi-fetch 用的路径（{id} 原样保留）。
func (m *Model) APIPath(a *ActionM) string { return a.Path }

// Permission 是操作的权限码。
func (m *Model) Permission(op string) string { return m.N.Permission + ":" + op }

// Display 是代表一条记录的字段。
func (m *Model) Display() *FieldM { return m.byName(m.D.Display) }

// NavGroup、NavIcon、NavOrder 是侧边栏菜单项。
func (m *Model) NavGroup() string { return cmp.Or(m.D.Admin.Nav.Group, "业务") }
func (m *Model) NavIcon() string  { return cmp.Or(m.D.Admin.Nav.Icon, "Table") }
func (m *Model) NavOrder() int64 {
	if o := m.D.Admin.Nav.Order; o != nil {
		return *o
	}
	return 100
}

// HasWrites 报告有没有写操作（新建、修改、删除、动作），没有时不生成 mutation。
func (m *Model) HasWrites() bool { return m.HasForm() || m.HasDelete || len(m.Actions) > 0 }

// HasForm 报告要不要生成表单（新建或编辑）。
func (m *Model) HasForm() bool { return m.HasCreate || m.HasUpdate }

// HasDetailActions 报告详情页上有没有操作按钮（编辑、删除、动作）。
func (m *Model) HasDetailActions() bool { return m.HasUpdate || m.HasDelete || len(m.Actions) > 0 }

// ListColumns 是列表的列：描述文件里的字段加上 created_at、updated_at（当作 datetime）。
func (m *Model) ListColumns() []*FieldM {
	var out []*FieldM
	for _, c := range m.D.Admin.List.Columns {
		out = append(out, m.SortField(c))
	}
	return out
}

// SystemTitle 是系统时间字段的中文名（和 spec 里的 title 一致）。
func SystemTitle(name string) string {
	return map[string]string{"created_at": "创建时间", "updated_at": "更新时间"}[name]
}

// DetailFields 是详情页显示的字段：全部字段，再加两个系统时间。
func (m *Model) DetailFields() []*FieldM {
	out := append([]*FieldM{}, m.Fields...)
	return append(out, m.SortField("created_at"), m.SortField("updated_at"))
}

// Cell 是在列表或详情里显示字段值的 JSX（v 是记录的变量名）。
func (m *Model) Cell(f *FieldM, v string) string {
	x := v + "." + f.Name
	switch f.Type {
	case "string":
		return fmt.Sprintf("<TextCell value={%s} />", x)
	case "text":
		return fmt.Sprintf("<TextCell value={%s} truncate />", x)
	case "int":
		return fmt.Sprintf("<NumberCell value={%s} />", x)
	case "money":
		return fmt.Sprintf("<MoneyCell cents={%s} />", x)
	case "bool":
		return fmt.Sprintf("<BoolCell value={%s} />", x)
	case "enum":
		return fmt.Sprintf("<EnumCell value={%s} labels={%s.labels} />", x, MetaVar(f.EnumType))
	case "date":
		return fmt.Sprintf("<DateCell value={%s} />", x)
	}
	return fmt.Sprintf("<DateTimeCell value={%s} />", x)
}

// DetailCell 同 Cell，长文本不截断、保留换行。
func (m *Model) DetailCell(f *FieldM, v string) string {
	if f.Type == "text" {
		return fmt.Sprintf(`<span className="whitespace-pre-wrap">{%s.%s || "—"}</span>`, v, f.Name)
	}
	return m.Cell(f, v)
}

// CellComponents 是列表或详情用到的单元格组件（import 用，按名称排序）。
func (m *Model) CellComponents(fields []*FieldM) string {
	seen := map[string]bool{}
	for _, f := range fields {
		seen[map[string]string{"string": "TextCell", "text": "TextCell", "int": "NumberCell", "money": "MoneyCell",
			"bool": "BoolCell", "enum": "EnumCell", "date": "DateCell", "datetime": "DateTimeCell"}[f.Type]] = true
	}
	return sortedJoin(seen)
}

// ListCellComponents 是列表用到的单元格组件：代表记录的字段是链接，不用单元格组件。
func (m *Model) ListCellComponents() string {
	var fs []*FieldM
	for _, f := range m.ListColumns() {
		if f.Name != m.D.Display {
			fs = append(fs, f)
		}
	}
	return m.CellComponents(fs)
}

// DetailCellComponents 同 CellComponents，用于详情页（长文本不用单元格组件）。
func (m *Model) DetailCellComponents() string {
	var fs []*FieldM
	for _, f := range m.DetailFields() {
		if f.Type != "text" {
			fs = append(fs, f)
		}
	}
	return m.CellComponents(fs)
}

// FormFields 是新建和编辑表单里的全部字段（import 用）。
func FormFields(m *Model) []*FieldM { return append(append([]*FieldM{}, m.Create...), m.Update...) }

// InputActions 是带参数的动作（各有一个对话框）。
func (m *Model) InputActions() []*ActionM {
	var out []*ActionM
	for _, a := range m.Actions {
		if len(a.Input) > 0 {
			out = append(out, a)
		}
	}
	return out
}

// ActionInputs 是全部动作的参数（import 用）。
func (m *Model) ActionInputs() []*FieldM {
	var out []*FieldM
	for _, a := range m.Actions {
		out = append(out, a.Input...)
	}
	return out
}

// UsesConfirmButton 报告详情页的操作里有没有确认框：没有参数的动作和删除用它。
func (m *Model) UsesConfirmButton() bool {
	return m.HasDelete || len(m.InputActions()) < len(m.Actions)
}

// ActionFieldErrors 报告有没有动作的错误要显示在字段下。
func (m *Model) ActionFieldErrors() bool {
	for _, a := range m.Actions {
		if m.FieldErrors(a.Name) != "" {
			return true
		}
	}
	return false
}

// FieldTitle 是字段标签的 TS 表达式：系统字段直接写中文（meta 里有，但这里不依赖它）。
func (m *Model) FieldTitle(f *FieldM) string {
	return m.EntityMeta() + ".fields." + f.Name + ".title"
}

// EnumMetas 是 fields 里引用的枚举 meta 变量（import 用）。
func (m *Model) EnumMetas(fields []*FieldM) []string {
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if f.EnumType != "" && !seen[f.EnumType] {
			seen[f.EnumType] = true
			out = append(out, MetaVar(f.EnumType))
		}
	}
	return out
}

// FormField 是表单里一个字段的 JSX：fields 是这个请求体的 meta 字段（如 createWidgetRequestMeta.fields）。
func (m *Model) FormField(f *FieldM, fields string, first bool) string {
	label := fields + "." + f.Name + ".title"
	focus := ""
	if first {
		focus = " autoFocus"
	}
	var c string
	switch f.Type {
	case "string":
		c = fmt.Sprintf(`<f.TextField label={%s} autoComplete="off"%s />`, label, focus)
	case "text":
		c = fmt.Sprintf(`<f.TextareaField label={%s} />`, label)
	case "int":
		c = fmt.Sprintf(`<f.NumberField label={%s}%s />`, label, focus)
	case "money":
		c = fmt.Sprintf(`<f.MoneyField label={%s}%s />`, label, focus)
	case "bool":
		c = fmt.Sprintf(`<f.SwitchField label={%s} />`, label)
	case "enum":
		clear := ""
		if f.Nullable {
			clear = " clearable"
		}
		c = fmt.Sprintf(`<f.SelectField label={%s} options={enumOptions(%s)}%s />`, label, MetaVar(f.EnumType), clear)
	case "date":
		c = fmt.Sprintf(`<f.DateField label={%s} />`, label)
	default:
		c = fmt.Sprintf(`<f.DateTimeField label={%s} />`, label)
	}
	return fmt.Sprintf(`<form.AppField name=%q>{(f) => %s}</form.AppField>`, f.Name, c)
}

// BlankValue 是新建表单里字段的初始值（输入框里是字符串，开关是布尔值），有 default 时填上它。
func (f *FieldM) BlankValue() string {
	def := ""
	if f.Default != nil {
		def = f.Default.String()
	}
	switch f.Type {
	case "bool":
		return strconv.FormatBool(def == "true")
	case "money":
		if def != "" {
			return fmt.Sprintf("centsToYuan(%s)", def)
		}
	}
	return strconv.Quote(def)
}

// EditValue 是编辑表单里字段的初始值：记录的值换成输入框里的写法。
func (f *FieldM) EditValue(v string) string {
	x := v + "." + f.Name
	switch f.Type {
	case "string", "text":
		return x
	case "bool":
		return x
	case "int":
		if f.Nullable {
			return fmt.Sprintf(`%s == null ? "" : String(%s)`, x, x)
		}
		return "String(" + x + ")"
	case "money":
		if f.Nullable {
			return fmt.Sprintf(`%s == null ? "" : centsToYuan(%s)`, x, x)
		}
		return "centsToYuan(" + x + ")"
	}
	if f.Nullable {
		return x + ` ?? ""`
	}
	return x
}

// UsesCentsToYuan 报告表单的初始值里有没有用到 centsToYuan（import 用）。
func (m *Model) UsesCentsToYuan() bool {
	for _, f := range m.Create {
		if f.Type == "money" && f.Default != nil {
			return true
		}
	}
	for _, f := range m.Update {
		if f.Type == "money" {
			return true
		}
	}
	return false
}

// UsesEnumOptions 报告表单里有没有下拉框（import enumOptions 用）。
func UsesEnumOptions(fields []*FieldM) bool {
	for _, f := range fields {
		if f.Type == "enum" {
			return true
		}
	}
	return false
}

// FieldErrors 是能归到某个字段的错误码 → 字段名（TS 对象字面量的内容），表单把它们显示在字段下。
// op 是 create、update 或动作名。
func (m *Model) FieldErrors(op string) string {
	var kv []string
	add := func(code, field string) { kv = append(kv, fmt.Sprintf("%s: %q", code, field)) }
	switch op {
	case "create", "update":
		fields := m.Create
		if op == "update" {
			fields = m.Update
		}
		for _, f := range m.Unique {
			if inFields(fields, f.Name) {
				add(m.N.Resource+"_"+f.Name+"_taken", f.Name)
			}
		}
		for _, r := range m.Rules {
			if (op == "create" && !r.OnCreate) || (op == "update" && !r.OnUpdate) {
				continue
			}
			for _, e := range r.Errors {
				if e.Field != "" && inFields(fields, e.Field) {
					add(e.Code, e.Field)
				}
			}
		}
	default:
		for _, a := range m.Actions {
			if a.Name != op {
				continue
			}
			// 声明式动作用参数写唯一字段（如改名）时，唯一约束冲突显示在参数下。
			for _, st := range a.Sets {
				if st.Param != nil && st.Field.Unique {
					add(m.N.Resource+"_"+st.Field.Name+"_taken", st.Param.Name)
				}
			}
			for _, e := range a.Errors {
				if e.Field != "" && inFields(a.Input, e.Field) {
					add(e.Code, e.Field)
				}
			}
		}
	}
	return strings.Join(kv, ", ")
}

func inFields(fs []*FieldM, name string) bool {
	for _, f := range fs {
		if f.Name == name {
			return true
		}
	}
	return false
}

// FieldErrorsVar 是字段错误映射的常量名，如 CREATE_FIELD_ERRORS、RENAME_FIELD_ERRORS。
func FieldErrorsVar(op string) string { return strings.ToUpper(op) + "_FIELD_ERRORS" }

// ActionShow 是动作按钮的显示条件：有权限，并且记录满足前置条件（v 是记录的变量名）。
func (m *Model) ActionShow(a *ActionM, v string) string {
	can := "can" + pascal(a.Name)
	if w := m.WhenTS(a, v); w != "true" {
		return can + " && " + w
	}
	return can
}

// WhenTS 是动作的前置条件在前端的写法（v 是记录的变量名），没有条件时是 "true"。
func (m *Model) WhenTS(a *ActionM, v string) string {
	var conds []string
	for _, w := range a.Whens {
		x := v + "." + w.Field.Name
		raw := w.Raw()
		if w.Field.Type == "bool" {
			if len(raw) == 1 {
				conds = append(conds, x+" === "+raw[0])
			}
			continue
		}
		if len(raw) == 1 {
			conds = append(conds, x+" === "+raw[0])
			continue
		}
		list := "[" + strings.Join(raw, ", ") + "]"
		if w.Field.Nullable {
			conds = append(conds, fmt.Sprintf("%s != null && %s.includes(%s)", x, list, x))
		} else {
			conds = append(conds, fmt.Sprintf("%s.includes(%s)", list, x))
		}
	}
	if len(conds) == 0 {
		return "true"
	}
	return strings.Join(conds, " && ")
}

// FilterRanges 是日期范围筛选的字段（URL 里是 yyyy-mm-dd，请求前换算）。
func (m *Model) FilterRanges() []*FieldM {
	var out []*FieldM
	for _, f := range m.Filters {
		if f.Type == "date" || f.Type == "datetime" {
			out = append(out, f)
		}
	}
	return out
}

// FilterTitle 是筛选控件的标签。
func (m *Model) FilterTitle(f *FieldM) string {
	if t := SystemTitle(f.Name); t != "" {
		return t
	}
	return f.Title
}

// SortOptions 是排序下拉框的选项（TS 数组字面量的内容）。
func (m *Model) SortOptions() string {
	var opts []string
	for _, s := range m.SortFields {
		title := m.FilterTitle(m.SortField(s))
		opts = append(opts, fmt.Sprintf(`{ value: %q, label: %q }`, "-"+s, title+"降序"), fmt.Sprintf(`{ value: %q, label: %q }`, s, title+"升序"))
	}
	return strings.Join(opts, ", ")
}

// DefaultSortValue 是默认排序在 sort 参数里的写法。
func (m *Model) DefaultSortValue() string {
	if m.DefaultDesc {
		return "-" + m.DefaultSort
	}
	return m.DefaultSort
}

// SearchPlaceholder 是关键字搜索框的提示文字。
func (m *Model) SearchPlaceholder() string { return "按" + titles(m.Search) + "搜索" }

func titles(fs []*FieldM) string {
	var ts []string
	for _, f := range fs {
		ts = append(ts, f.Title)
	}
	return strings.Join(ts, "、")
}

// FilterComponents 是列表页用到的筛选组件（import 用）。
func (m *Model) FilterComponents() string {
	seen := map[string]bool{"FilterBar": true}
	if len(m.Search) > 0 {
		seen["SearchInput"] = true
	}
	if len(m.SortFields) > 1 {
		seen["SortSelect"] = true
	}
	for _, f := range m.Filters {
		switch f.Type {
		case "enum":
			seen["EnumFilter"] = true
		case "bool":
			seen["BoolFilter"] = true
		default:
			seen["DateRangeFilter"] = true
		}
	}
	return sortedJoin(seen)
}

// HasFilterBar 报告列表页有没有筛选栏。
func (m *Model) HasFilterBar() bool {
	return len(m.Search) > 0 || len(m.Filters) > 0 || len(m.SortFields) > 1
}

// UsesDayRange、UsesDateRange 报告请求前要不要换算日期范围（datetime 按浏览器时区的日界，date 只把止日加一天）。
func (m *Model) UsesDayRange() bool  { return m.hasFilterType("datetime") }
func (m *Model) UsesDateRange() bool { return m.hasFilterType("date") }

func (m *Model) hasFilterType(t string) bool {
	for _, f := range m.Filters {
		if f.Type == t {
			return true
		}
	}
	return false
}

// HasEnumFilters 报告列表页有没有枚举筛选（import enumOptions 用）。
func (m *Model) HasEnumFilters() bool { return m.hasFilterType("enum") }

func sortedJoin(set map[string]bool) string {
	var out []string
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}

// jsString 是 JS 字符串字面量（JSON 的写法，不转义 < > &）。模板里写成 JSX 表达式 attr={...}：
// JSX 属性的字符串字面量不处理反斜杠转义。
func jsString(s string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSuffix(b.String(), "\n")
}
