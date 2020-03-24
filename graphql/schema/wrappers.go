/*
 * Copyright 2019 Dgraph Labs, Inc. and Contributors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package schema

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/dgraph-io/dgraph/gql"
	"github.com/dgraph-io/dgraph/x"
	"github.com/pkg/errors"
	"github.com/vektah/gqlparser/v2/ast"
)

// Wrap the github.com/vektah/gqlparser/ast defintions so that the bulk of the GraphQL
// algorithm and interface is dependent on behaviours we expect from a GraphQL schema
// and validation, but not dependent the exact structure in the gqlparser.
//
// This also auto hooks up some bookkeeping that's otherwise no fun.  E.g. getting values for
// field arguments requires the variable map from the operation - so we'd need to carry vars
// through all the resolver functions.  Much nicer if they are resolved by magic here.

// QueryType is currently supported queries
type QueryType string

// MutationType is currently supported mutations
type MutationType string

// Query/Mutation types and arg names
const (
	GetQuery             QueryType    = "get"
	FilterQuery          QueryType    = "query"
	SchemaQuery          QueryType    = "schema"
	PasswordQuery        QueryType    = "checkPassword"
	NotSupportedQuery    QueryType    = "notsupported"
	AddMutation          MutationType = "add"
	UpdateMutation       MutationType = "update"
	DeleteMutation       MutationType = "delete"
	NotSupportedMutation MutationType = "notsupported"
	IDType                            = "ID"
	IDArgName                         = "id"
	InputArgName                      = "input"
	FilterArgName                     = "filter"
)

// Schema represents a valid GraphQL schema
type Schema interface {
	Operation(r *Request) (Operation, error)
	Queries(t QueryType) []string
	Mutations(t MutationType) []string
	AuthTypeRules(typeName string) *AuthContainer
	AuthFieldRules(typeName, fieldName string) *AuthContainer
}

// An Operation is a single valid GraphQL operation.  It contains either
// Queries or Mutations, but not both.  Subscriptions are not yet supported.
type Operation interface {
	Queries() []Query
	Mutations() []Mutation
	Schema() Schema
	IsQuery() bool
	IsMutation() bool
	IsSubscription() bool
}

// A Field is one field from an Operation.
type Field interface {
	Name() string
	Alias() string
	ResponseName() string
	ArgValue(name string) interface{}
	IsArgListType(name string) bool
	IDArgValue() (*string, uint64, error)
	XIDArg() string
	SetArgTo(arg string, val interface{})
	Skip() bool
	Include() bool
	Type() Type
	SelectionSet() []Field
	Location() x.Location
	DgraphPredicate() string
	Operation() Operation
	// InterfaceType tells us whether this field represents a GraphQL Interface.
	InterfaceType() bool
	IncludeInterfaceField(types []interface{}) bool
	TypeName(dgraphTypes []interface{}) string
	GetObjectName() string
}

// A Mutation is a field (from the schema's Mutation type) from an Operation
type Mutation interface {
	Field
	MutationType() MutationType
	MutatedType() Type
	QueryField() Field
}

// A Query is a field (from the schema's Query type) from an Operation
type Query interface {
	Field
	QueryType() QueryType
	Rename(newName string)
}

// A Type is a GraphQL type like: Float, T, T! and [T!]!.  If it's not a list, then
// ListType is nil.  If it's an object type then Field gets field definitions by
// name from the definition of the type; IDField gets the ID field of the type.
type Type interface {
	Field(name string) FieldDefinition
	Fields() []FieldDefinition
	IDField() FieldDefinition
	XIDField() FieldDefinition
	PasswordField() FieldDefinition
	Name() string
	DgraphName() string
	DgraphPredicate(fld string) string
	Nullable() bool
	ListType() Type
	Interfaces() []string
	EnsureNonNulls(map[string]interface{}, string) error
	fmt.Stringer
}

// A FieldDefinition is a field as defined in some Type in the schema.  As opposed
// to a Field, which is an instance of a query or mutation asking for a field
// (which in turn must have a FieldDefinition of the right type in the schema.)
type FieldDefinition interface {
	Name() string
	Type() Type
	IsID() bool
	Inverse() FieldDefinition
	// TODO - It might be possible to get rid of ForwardEdge and just use Inverse() always.
	ForwardEdge() FieldDefinition
}

type astType struct {
	typ             *ast.Type
	inSchema        *ast.Schema
	dgraphPredicate map[string]map[string]string
}

type schema struct {
	schema *ast.Schema
	// dgraphPredicate gives us the dgraph predicate corresponding to a typeName + fieldName.
	// It is pre-computed so that runtime queries and mutations can look it
	// up quickly.
	// The key for the first map are the type names. The second map has a mapping of the
	// fieldName => dgraphPredicate.
	dgraphPredicate map[string]map[string]string
	// Map of mutation field name to mutated type.
	mutatedType map[string]*astType
	// Map from typename to ast.Definition
	typeNameAst map[string][]*ast.Definition
	// Map from typename to auth rules
	authRules map[string]*TypeAuth
}

type operation struct {
	op   *ast.OperationDefinition
	vars map[string]interface{}

	// The fields below are used by schema introspection queries.
	query    string
	doc      *ast.QueryDocument
	inSchema *schema
}

type field struct {
	field *ast.Field
	op    *operation
	sel   ast.Selection
	// arguments contains the computed values for arguments taking into account the values
	// for the GraphQL variables supplied in the query.
	arguments map[string]interface{}
}

type fieldDefinition struct {
	fieldDef        *ast.FieldDefinition
	inSchema        *ast.Schema
	dgraphPredicate map[string]map[string]string
}

type mutation field
type query field

func (s *schema) Queries(t QueryType) []string {
	var result []string
	for _, q := range s.schema.Query.Fields {
		if queryType(q.Name) == t {
			result = append(result, q.Name)
		}
	}
	return result
}

func (s *schema) Mutations(t MutationType) []string {
	var result []string
	for _, m := range s.schema.Mutation.Fields {
		if mutationType(m.Name) == t {
			result = append(result, m.Name)
		}
	}
	return result
}

func (s *schema) AuthTypeRules(typeName string) *AuthContainer {
	val := s.authRules[typeName]
	if val == nil {
		return nil
	}
	return s.authRules[typeName].rules
}

func (s *schema) AuthFieldRules(typeName, fieldName string) *AuthContainer {
	val := s.authRules[typeName]
	if val == nil {
		return nil
	}
	return s.authRules[typeName].fields[fieldName]
}

func (o *operation) IsQuery() bool {
	return o.op.Operation == ast.Query
}

func (o *operation) IsMutation() bool {
	return o.op.Operation == ast.Mutation
}

func (o *operation) IsSubscription() bool {
	return o.op.Operation == ast.Subscription
}

func (o *operation) Schema() Schema {
	return o.inSchema
}

func (o *operation) Queries() (qs []Query) {
	if !o.IsQuery() {
		return
	}

	for _, s := range o.op.SelectionSet {
		if f, ok := s.(*ast.Field); ok {
			qs = append(qs, &query{field: f, op: o, sel: s})
		}
	}

	return
}

func (o *operation) Mutations() (ms []Mutation) {
	if !o.IsMutation() {
		return
	}

	for _, s := range o.op.SelectionSet {
		if f, ok := s.(*ast.Field); ok {
			ms = append(ms, &mutation{field: f, op: o})
		}
	}

	return
}

// parentInterface returns the name of an interface that a field belonging to a type definition
// typDef inherited from. If there is no such interface, then it returns an empty string.
//
// Given the following schema
// interface A {
//   name: String
// }
//
// type B implements A {
//	 name: String
//   age: Int
// }
//
// calling parentInterface on the fieldName name with type definition for B, would return A.
func parentInterface(sch *ast.Schema, typDef *ast.Definition, fieldName string) *ast.Definition {
	if len(typDef.Interfaces) == 0 {
		return nil
	}

	for _, iface := range typDef.Interfaces {
		interfaceDef := sch.Types[iface]
		for _, interfaceField := range interfaceDef.Fields {
			if fieldName == interfaceField.Name {
				return interfaceDef
			}
		}
	}
	return nil
}

func convertPasswordDirective(dir *ast.Directive) *ast.FieldDefinition {
	if dir.Name != "secret" {
		return nil
	}

	name := dir.Arguments.ForName("field").Value.Raw
	pred := dir.Arguments.ForName("pred")
	dirs := ast.DirectiveList{}

	if pred != nil {
		dirs = ast.DirectiveList{{
			Name: "dgraph",
			Arguments: ast.ArgumentList{{
				Name: "pred",
				Value: &ast.Value{
					Raw:  pred.Value.Raw,
					Kind: ast.StringValue,
				},
			}},
			Position: dir.Position,
		}}
	}

	fd := &ast.FieldDefinition{
		Name: name,
		Type: &ast.Type{
			NamedType: "String",
			NonNull:   true,
			Position:  dir.Position,
		},
		Directives: dirs,
		Position:   dir.Position,
	}

	return fd
}

func dgraphMapping(sch *ast.Schema) map[string]map[string]string {
	const (
		add     = "Add"
		update  = "Update"
		del     = "Delete"
		payload = "Payload"
	)

	dgraphPredicate := make(map[string]map[string]string)
	for _, inputTyp := range sch.Types {
		// We only want to consider input types (object and interface) defined by the user as part
		// of the schema hence we ignore BuiltIn, query and mutation types.
		if inputTyp.BuiltIn || inputTyp.Name == "Query" || inputTyp.Name == "Mutation" ||
			(inputTyp.Kind != ast.Object && inputTyp.Kind != ast.Interface) {
			continue
		}

		originalTyp := inputTyp
		inputTypeName := inputTyp.Name
		if strings.HasPrefix(inputTypeName, add) && strings.HasSuffix(inputTypeName, payload) {
			continue
		}

		dgraphPredicate[originalTyp.Name] = make(map[string]string)

		if (strings.HasPrefix(inputTypeName, update) || strings.HasPrefix(inputTypeName, del)) &&
			strings.HasSuffix(inputTypeName, payload) {
			// For UpdateTypePayload and DeleteTypePayload, inputTyp should be Type.
			if strings.HasPrefix(inputTypeName, update) {
				inputTypeName = strings.TrimSuffix(strings.TrimPrefix(inputTypeName, update),
					payload)
			} else if strings.HasPrefix(inputTypeName, del) {
				inputTypeName = strings.TrimSuffix(strings.TrimPrefix(inputTypeName, del), payload)
			}
			inputTyp = sch.Types[inputTypeName]
		}

		// We add password field to the cached type information to be used while opening
		// resolving and rewriting queries to be sent to dgraph. Otherwise, rewriter won't
		// know what the password field in AddInputType/ TypePatch/ TypeRef is.
		var fields ast.FieldList
		fields = append(fields, inputTyp.Fields...)
		for _, directive := range inputTyp.Directives {
			fd := convertPasswordDirective(directive)
			if fd == nil {
				continue
			}
			fields = append(fields, fd)
		}

		for _, fld := range fields {
			if isID(fld) {
				// We don't need a mapping for the field, as we the dgraph predicate for them is
				// fixed i.e. uid.
				continue
			}
			typName := typeName(inputTyp)
			parentInt := parentInterface(sch, inputTyp, fld.Name)
			if parentInt != nil {
				typName = typeName(parentInt)
			}
			// 1. For fields that have @dgraph(pred: xxxName) directive, field name would be
			//    xxxName.
			// 2. For fields where the type (or underlying interface) has a @dgraph(type: xxxName)
			//    directive, field name would be xxxName.fldName.
			//
			// The cases below cover the cases where neither the type or field have @dgraph
			// directive.
			// 3. For types which don't inherit from an interface the keys, value would be.
			//    typName,fldName => typName.fldName
			// 4. For types which inherit fields from an interface
			//    typName,fldName => interfaceName.fldName
			// 5. For DeleteTypePayload type
			//    DeleteTypePayload,fldName => typName.fldName

			fname := fieldName(fld, typName)
			dgraphPredicate[originalTyp.Name][fld.Name] = fname
		}
	}
	return dgraphPredicate
}

func mutatedTypeMapping(s *ast.Schema,
	dgraphPredicate map[string]map[string]string) map[string]*astType {
	if s.Mutation == nil {
		return nil
	}

	m := make(map[string]*astType, len(s.Mutation.Fields))
	for _, field := range s.Mutation.Fields {
		mutatedTypeName := ""
		switch {
		case strings.HasPrefix(field.Name, "add"):
			mutatedTypeName = strings.TrimPrefix(field.Name, "add")
		case strings.HasPrefix(field.Name, "update"):
			mutatedTypeName = strings.TrimPrefix(field.Name, "update")
		case strings.HasPrefix(field.Name, "delete"):
			mutatedTypeName = strings.TrimPrefix(field.Name, "delete")
		default:
		}
		// This is a convoluted way of getting the type for mutatedTypeName. We get the definition
		// for UpdateTPayload and get the type from the first field. There is no direct way to get
		// the type from the definition of an object. We use Update and not Add here because
		// Interfaces only have Update.
		var def *ast.Definition
		if def = s.Types["Update"+mutatedTypeName+"Payload"]; def == nil {
			def = s.Types["Add"+mutatedTypeName+"Payload"]
		}

		if def == nil {
			continue
		}

		// Accessing 0th element should be safe to do as according to the spec an object must define
		// one or more fields.
		typ := def.Fields[0].Type
		// This would contain mapping of mutation field name to the Type()
		// for e.g. addPost => astType for Post
		m[field.Name] = &astType{typ, s, dgraphPredicate}
	}
	return m
}

func typeMappings(s *ast.Schema) map[string][]*ast.Definition {
	typeNameAst := make(map[string][]*ast.Definition)

	for _, typ := range s.Types {
		name := typeName(typ)
		typeNameAst[name] = append(typeNameAst[name], typ)
	}

	return typeNameAst
}

type AuthVariable int

const (
	Constant AuthVariable = iota
	Op
	JwtVar
	GqlTyp
)

type AuthOp int

const (
	Filter AuthOp = iota
	Predicate
	Jwt
)

type RuleAst struct {
	Name  string
	Typ   AuthVariable
	Value *RuleAst

	dgraphPredicate string
	typInfo         *ast.Definition
}

var operations = map[string]bool{
	"filter": true,
	"eq":     true,
}

var punctuations = map[byte]bool{
	'{': true,
	'}': true,
	':': true,
	'(': true,
	')': true,
	' ': true,
}

type Parser struct {
	index int
	str   *string
}

func (p *Parser) init(str *string) {
	p.str = str
	p.index = 0
}

func (p *Parser) skipPunc() {
	for ; p.index != len(*p.str) && punctuations[(*p.str)[p.index]]; p.index += 1 {
	}
}

func (p *Parser) getNextWord() string {
	start := p.index
	for ; p.index != len(*p.str) && !punctuations[(*p.str)[p.index]]; p.index += 1 {
	}

	word := (*p.str)[start:p.index]
	p.skipPunc()

	return word
}

func (p *Parser) isEmpty() bool {
	return p.index == len(*p.str)
}

func (ap *AuthParser) buildRuleAST(rule string, dgraphPredicate map[string]map[string]string) *RuleAst {
	var ast *RuleAst
	var p Parser

	builder := &ast
	typ := ap.currentTyp

	p.init(&rule)
	p.skipPunc()

	for !p.isEmpty() {
		rule := &RuleAst{}
		word := p.getNextWord()

		rule.Name = word
		rule.typInfo = typ

		if word[0] == '$' {
			rule.Typ = JwtVar
			rule.Name = word[1:]
		} else if operations[word] {
			rule.Typ = Op
		} else if field := typ.Fields.ForName(word); field != nil {
			name := field.Type.Name()
			rule.Typ = GqlTyp
			rule.dgraphPredicate = dgraphPredicate[typ.Name][field.Name]
			typ = ap.s.Types[name]
		} else {
			rule.Typ = Constant
		}

		*builder = rule
		builder = &rule.Value
	}

	return ast
}

type RuleNode struct {
	RuleID int
	Or     []*RuleNode
	And    []*RuleNode
	Not    *RuleNode

	Rule *RuleAst
}

func (r *RuleNode) GetFilter() *gql.FilterTree {
	result := &gql.FilterTree{}
	if len(r.Or) > 0 || len(r.And) > 0 {
		result.Op = "or"
		if len(r.And) > 0 {
			result.Op = "and"
		}
		for _, i := range r.Or {
			t := i.GetFilter()
			if t == nil {
				continue
			}
			result.Child = append(result.Child, t)
		}

		return result
	}

	if r.Not != nil {
		// TODO reverse
		return r.Not.GetFilter()
	}

	if r.isRBAC() {
		return nil
	}

	if r.Rule.IsFilter() {
		result.Func = &gql.Function{
			Name: "uid",
			Args: []gql.Arg{{
				Value: fmt.Sprintf("rule_%s_%d", r.Rule.getName(), r.RuleID),
			}},
		}

		return result
	}

	return r.Rule.getRuleQuery()
}

type AuthContainer struct {
	Query  *RuleNode
	Add    *RuleNode
	Update *RuleNode
	Delete *RuleNode
}

func (r *RuleAst) checkType(op AuthVariable) bool {
	if r == nil {
		return false
	}

	return r.Typ == op
}

func (r *RuleAst) IsGqlTyp() bool {
	return r.checkType(GqlTyp)
}

func (r *RuleAst) IsConstant() bool {
	return r.checkType(Constant)
}

func (r *RuleAst) IsJWT() bool {
	return r.checkType(JwtVar)
}

func (r *RuleAst) IsOp() bool {
	return r.checkType(Op)
}

func (r *RuleAst) GetOperation() *RuleAst {
	if r == nil {
		return nil
	}

	switch r.Typ {
	case Constant:
		// Constant should be a leaf
		return nil
	case Op:
		// Already an operation
		return r
	case GqlTyp:
	case JwtVar:
		if r.Value != nil && r.Value.IsOp() {
			return r.Value
		}
	}

	return nil
}

func (r *RuleAst) GetOperand() *RuleAst {
	if r == nil && !r.IsOp() {
		return nil
	}

	return r.Value
}

func (r *RuleAst) getName() string {
	if r.Typ == GqlTyp {
		return r.dgraphPredicate
	}

	if r.Typ == JwtVar {
		return "user1"
	}

	return r.Name
}

// Builds query used to get authorized nodes and their uids
func (r *RuleAst) buildQuery(ruleID int) *gql.GraphQuery {
	operation := r.GetOperation()
	if operation.getName() != "filter" {
		return nil
	}

	operand := operation.GetOperand()

	dgQuery := &gql.GraphQuery{
		Cascade: true,
		Attr:    fmt.Sprintf("rule_%s_%d", r.getName(), ruleID),
		Var:     fmt.Sprintf("rule_%s_%d", r.getName(), ruleID),
		Func: &gql.Function{
			Name: "type",
			Args: []gql.Arg{{Value: r.typInfo.Name}},
		},
		Children: []*gql.GraphQuery{
			{Attr: "uid"},
			{
				Attr:     r.getName(),
				Children: []*gql.GraphQuery{{Attr: "uid"}},
				Filter:   operand.getRuleQuery(),
			},
		},
	}
	return dgQuery
}

// Creates a filter() for dgraph operations, like, eq
func (r *RuleAst) getRuleQuery() *gql.FilterTree {
	operation := r.GetOperation()
	if operation.IsFilter() {
		return nil
	}

	return &gql.FilterTree{
		Func: &gql.Function{
			Name: operation.getName(),
			Args: []gql.Arg{
				{Value: r.getName()},
				{Value: operation.GetOperand().getName()},
			},
		},
	}
}

func (r *RuleAst) IsFilter() bool {
	if r == nil {
		return false
	}

	operation := r.GetOperation()
	return operation.getName() == "filter"
}

func (r *RuleNode) GetQueries() []*gql.GraphQuery {
	var list []*gql.GraphQuery

	for _, i := range r.Or {
		list = append(list, i.GetQueries()...)
	}

	for _, i := range r.And {
		list = append(list, i.GetQueries()...)
	}

	if r.Not != nil {
		// TODO reverse sign
		list = append(list, r.Not.GetQueries()...)
	}

	if r.Rule != nil {
		if query := r.Rule.buildQuery(r.RuleID); query != nil {
			list = append(list, query)
		}
	}

	return list
}

func (r *RuleNode) isRBAC() bool {
	for _, i := range r.Or {
		if i.isRBAC() {
			return true
		}
	}
	for _, i := range r.And {
		if !i.isRBAC() {
			return false
		}
		return true
	}

	if r.Not != nil && r.Not.isRBAC() {
		return true
	}

	rule := r.Rule
	for rule != nil {
		if rule.Typ == GqlTyp {
			return false
		}
		rule = rule.Value
	}

	return true
}

func (c *AuthContainer) isRBAC() bool {
	if c.Query != nil && c.Query.isRBAC() {
		return true
	}
	if c.Add != nil && c.Add.isRBAC() {
		return true
	}
	if c.Update != nil && c.Update.isRBAC() {
		return true
	}
	if c.Delete != nil && c.Delete.isRBAC() {
		return true
	}

	return false
}

type AuthParser struct {
	s *ast.Schema

	currentTyp      *ast.Definition
	ruleId          int
	dgraphPredicate *map[string]map[string]string
}

func (ap *AuthParser) parseRules(rule map[string]interface{}) *RuleNode {
	var ruleNode RuleNode
	ruleNode.RuleID = ap.ruleId
	ap.ruleId++

	or, ok := rule["or"].([]interface{})
	if ok {
		for _, node := range or {
			ruleNode.Or = append(ruleNode.Or,
				ap.parseRules(node.(map[string]interface{})))
		}
	}

	and, ok := rule["and"].([]interface{})
	if ok {
		for _, node := range and {
			ruleNode.And = append(ruleNode.And,
				ap.parseRules(node.(map[string]interface{})))
		}
	}

	not, ok := rule["not"].(map[string]interface{})
	if ok {
		ruleNode.Not = ap.parseRules(not)
	}

	ruleString, ok := rule["rule"].(string)
	if ok {
		ruleNode.Rule = ap.buildRuleAST(ruleString, *ap.dgraphPredicate)
	}

	return &ruleNode
}

func (ap *AuthParser) parseAuthDirective(directive map[string]interface{}) *AuthContainer {
	var container AuthContainer

	query, ok := directive["query"].(map[string]interface{})
	if ok {
		container.Query = ap.parseRules(query)
	}

	add, ok := directive["add"].(map[string]interface{})
	if ok {
		container.Add = ap.parseRules(add)
	}

	update, ok := directive["update"].(map[string]interface{})
	if ok {
		container.Update = ap.parseRules(update)
	}

	delete, ok := directive["delete"].(map[string]interface{})
	if ok {
		container.Delete = ap.parseRules(delete)
	}

	return &container
}

type TypeAuth struct {
	rules  *AuthContainer
	fields map[string]*AuthContainer
}

func authRules(s *ast.Schema, dgraphPredicate *map[string]map[string]string) map[string]*TypeAuth {
	authRules := make(map[string]*TypeAuth)
	var emptyMap map[string]interface{}
	var p AuthParser

	p.s = s
	p.dgraphPredicate = dgraphPredicate

	for _, typ := range s.Types {
		name := typeName(typ)
		auth := typ.Directives.ForName("auth")
		p.currentTyp = typ
		p.ruleId = 1
		authRules[name] = &TypeAuth{fields: make(map[string]*AuthContainer)}

		if auth != nil {
			authRules[name].rules = p.parseAuthDirective(auth.ArgumentMap(emptyMap))

		}

		for _, field := range typ.Fields {
			auth := field.Directives.ForName("auth")
			if auth != nil {
				authRules[name].fields[field.Name] =
					p.parseAuthDirective(auth.ArgumentMap(emptyMap))
			}
		}
	}

	return authRules
}

// AsSchema wraps a github.com/vektah/gqlparser/ast.Schema.
func AsSchema(s *ast.Schema) Schema {
	dgraphPredicate := dgraphMapping(s)
	return &schema{
		schema:          s,
		dgraphPredicate: dgraphPredicate,
		mutatedType:     mutatedTypeMapping(s, dgraphPredicate),
		typeNameAst:     typeMappings(s),
		authRules:       authRules(s, &dgraphPredicate),
	}
}

func responseName(f *ast.Field) string {
	if f.Alias == "" {
		return f.Name
	}
	return f.Alias
}

func (f *field) Name() string {
	return f.field.Name
}

func (f *field) Alias() string {
	return f.field.Alias
}

func (f *field) ResponseName() string {
	return responseName(f.field)
}

func (f *field) SetArgTo(arg string, val interface{}) {
	if f.arguments == nil {
		f.arguments = make(map[string]interface{})
	}
	f.arguments[arg] = val

	// If the argument doesn't exist, add it to the list. It is used later on to get
	// parameters. Value isn't required because it's fetched using the arguments map.
	argument := f.field.Arguments.ForName(arg)
	if argument == nil {
		f.field.Arguments = append(f.field.Arguments, &ast.Argument{Name: arg})
	}
}

func (f *field) ArgValue(name string) interface{} {
	if f.arguments == nil {
		// Compute and cache the map first time this function is called for a field.
		f.arguments = f.field.ArgumentMap(f.op.vars)
	}
	return f.arguments[name]
}

func (f *field) IsArgListType(name string) bool {
	arg := f.field.Arguments.ForName(name)
	if arg == nil {
		return false
	}

	return arg.Value.ExpectedType.Elem != nil
}

func (f *field) Skip() bool {
	dir := f.field.Directives.ForName("skip")
	if dir == nil {
		return false
	}
	return dir.ArgumentMap(f.op.vars)["if"].(bool)
}

func (f *field) Include() bool {
	dir := f.field.Directives.ForName("include")
	if dir == nil {
		return true
	}
	return dir.ArgumentMap(f.op.vars)["if"].(bool)
}

func (f *field) XIDArg() string {
	xidArgName := ""
	passwordField := f.Type().PasswordField()
	for _, arg := range f.field.Arguments {
		if arg.Name != IDArgName && (passwordField == nil ||
			arg.Name != passwordField.Name()) {
			xidArgName = arg.Name
		}
	}
	return f.Type().DgraphPredicate(xidArgName)
}

func (f *field) IDArgValue() (xid *string, uid uint64, err error) {
	idField := f.Type().IDField()
	passwordField := f.Type().PasswordField()
	xidArgName := ""
	// This method is only called for Get queries and check. These queries can accept ID, XID
	// or Password. Therefore the non ID and Password field is an XID.
	// TODO maybe there is a better way to do this.
	for _, arg := range f.field.Arguments {
		if (idField == nil || arg.Name != idField.Name()) &&
			(passwordField == nil || arg.Name != passwordField.Name()) {
			xidArgName = arg.Name
		}
	}
	if xidArgName != "" {
		xidArgVal, ok := f.ArgValue(xidArgName).(string)
		pos := f.field.GetPosition()
		if !ok {
			err = x.GqlErrorf("Argument (%s) of %s was not able to be parsed as a string",
				xidArgName, f.Name()).WithLocations(x.Location{Line: pos.Line, Column: pos.Column})
			return
		}
		xid = &xidArgVal
	}

	if idField == nil {
		return
	}

	idArg := f.ArgValue(idField.Name())
	if idArg != nil {
		id, ok := idArg.(string)
		var ierr error
		uid, ierr = strconv.ParseUint(id, 0, 64)

		if !ok || ierr != nil {
			pos := f.field.GetPosition()
			err = x.GqlErrorf("ID argument (%s) of %s was not able to be parsed", id, f.Name()).
				WithLocations(x.Location{Line: pos.Line, Column: pos.Column})
			return
		}
	}

	return
}

func (f *field) Type() Type {
	return &astType{
		typ:             f.field.Definition.Type,
		inSchema:        f.op.inSchema.schema,
		dgraphPredicate: f.op.inSchema.dgraphPredicate,
	}
}

func (f *field) InterfaceType() bool {
	return f.op.inSchema.schema.Types[f.field.Definition.Type.Name()].Kind == ast.Interface
}

func (f *field) GetObjectName() string {
	return f.field.ObjectDefinition.Name
}

func (f *field) SelectionSet() (flds []Field) {
	for _, s := range f.field.SelectionSet {
		if fld, ok := s.(*ast.Field); ok {
			flds = append(flds, &field{
				field: fld,
				op:    f.op,
			})
		}
	}

	return
}

func (f *field) Location() x.Location {
	return x.Location{
		Line:   f.field.Position.Line,
		Column: f.field.Position.Column}
}

func (f *field) Operation() Operation {
	return f.op
}

func (f *field) DgraphPredicate() string {
	return f.op.inSchema.dgraphPredicate[f.field.ObjectDefinition.Name][f.Name()]
}

func (f *field) TypeName(dgraphTypes []interface{}) string {
	for _, typ := range dgraphTypes {
		styp, ok := typ.(string)
		if !ok {
			continue
		}

		for _, origTyp := range f.op.inSchema.typeNameAst[styp] {
			if origTyp.Kind != ast.Object {
				continue
			}
			return origTyp.Name
		}

	}
	return ""
}

func (f *field) IncludeInterfaceField(dgraphTypes []interface{}) bool {
	// As ID maps to uid in dgraph, so it is not stored as an edge, hence does not appear in
	// f.op.inSchema.dgraphPredicate map. So, always include the queried field if it is of ID type.
	if f.Type().Name() == IDType {
		return true
	}
	// Given a list of dgraph types, we query the schema and find the one which is an ast.Object
	// and not an Interface object.
	for _, typ := range dgraphTypes {
		styp, ok := typ.(string)
		if !ok {
			continue
		}
		for _, origTyp := range f.op.inSchema.typeNameAst[styp] {
			if origTyp.Kind == ast.Object {
				// If the field doesn't exist in the map corresponding to the object type, then we
				// don't need to include it.
				_, ok := f.op.inSchema.dgraphPredicate[origTyp.Name][f.Name()]
				return ok || f.Name() == Typename
			}
		}

	}
	return false
}

func (q *query) Rename(newName string) {
	q.field.Name = newName
}

func (q *query) Name() string {
	return (*field)(q).Name()
}

func (q *query) Alias() string {
	return (*field)(q).Alias()
}

func (q *query) SetArgTo(arg string, val interface{}) {
	(*field)(q).SetArgTo(arg, val)
}

func (q *query) ArgValue(name string) interface{} {
	return (*field)(q).ArgValue(name)
}

func (q *query) IsArgListType(name string) bool {
	return (*field)(q).IsArgListType(name)
}

func (q *query) Skip() bool {
	return false
}

func (q *query) Include() bool {
	return true
}

func (q *query) IDArgValue() (*string, uint64, error) {
	return (*field)(q).IDArgValue()
}

func (q *query) XIDArg() string {
	return (*field)(q).XIDArg()
}

func (q *query) Type() Type {
	return (*field)(q).Type()
}

func (q *query) SelectionSet() []Field {
	return (*field)(q).SelectionSet()
}

func (q *query) Location() x.Location {
	return (*field)(q).Location()
}

func (q *query) ResponseName() string {
	return (*field)(q).ResponseName()
}

func (q *query) GetObjectName() string {
	return q.field.ObjectDefinition.Name
}

func (q *query) QueryType() QueryType {
	return queryType(q.Name())
}

func queryType(name string) QueryType {
	switch {
	case strings.HasPrefix(name, "get"):
		return GetQuery
	case name == "__schema" || name == "__type":
		return SchemaQuery
	case strings.HasPrefix(name, "query"):
		return FilterQuery
	case strings.HasPrefix(name, "check"):
		return PasswordQuery
	default:
		return NotSupportedQuery
	}
}

func (q *query) Operation() Operation {
	return (*field)(q).Operation()
}

func (q *query) DgraphPredicate() string {
	return (*field)(q).DgraphPredicate()
}

func (q *query) InterfaceType() bool {
	return (*field)(q).InterfaceType()
}

func (q *query) TypeName(dgraphTypes []interface{}) string {
	return (*field)(q).TypeName(dgraphTypes)
}

func (q *query) IncludeInterfaceField(dgraphTypes []interface{}) bool {
	return (*field)(q).IncludeInterfaceField(dgraphTypes)
}

func (m *mutation) Name() string {
	return (*field)(m).Name()
}

func (m *mutation) Alias() string {
	return (*field)(m).Alias()
}

func (m *mutation) SetArgTo(arg string, val interface{}) {
	(*field)(m).SetArgTo(arg, val)
}

func (m *mutation) IsArgListType(name string) bool {
	return (*field)(m).IsArgListType(name)
}

func (m *mutation) ArgValue(name string) interface{} {
	return (*field)(m).ArgValue(name)
}

func (m *mutation) Skip() bool {
	return false
}

func (m *mutation) Include() bool {
	return true
}

func (m *mutation) Type() Type {
	return (*field)(m).Type()
}

func (m *mutation) InterfaceType() bool {
	return (*field)(m).InterfaceType()
}

func (m *mutation) XIDArg() string {
	return (*field)(m).XIDArg()
}

func (m *mutation) IDArgValue() (*string, uint64, error) {
	return (*field)(m).IDArgValue()
}

func (m *mutation) SelectionSet() []Field {
	return (*field)(m).SelectionSet()
}

func (m *mutation) QueryField() Field {
	for _, i := range m.SelectionSet() {
		if i.Name() == NumUid {
			continue
		}
		return i
	}
	return m.SelectionSet()[0]
}

func (m *mutation) Location() x.Location {
	return (*field)(m).Location()
}

func (m *mutation) ResponseName() string {
	return (*field)(m).ResponseName()
}

// MutatedType returns the underlying type that gets mutated by m.
//
// It's not the same as the response type of m because mutations don't directly
// return what they mutate.  Mutations return a payload package that includes
// the actual node mutated as a field.
func (m *mutation) MutatedType() Type {
	// ATM there's a single field in the mutation payload.
	return m.op.inSchema.mutatedType[m.Name()]
}

func (m *mutation) GetObjectName() string {
	return m.field.ObjectDefinition.Name
}

func (m *mutation) MutationType() MutationType {
	return mutationType(m.Name())
}

func mutationType(name string) MutationType {
	switch {
	case strings.HasPrefix(name, "add"):
		return AddMutation
	case strings.HasPrefix(name, "update"):
		return UpdateMutation
	case strings.HasPrefix(name, "delete"):
		return DeleteMutation
	default:
		return NotSupportedMutation
	}
}

func (m *mutation) Operation() Operation {
	return (*field)(m).Operation()
}

func (m *mutation) DgraphPredicate() string {
	return (*field)(m).DgraphPredicate()
}

func (m *mutation) TypeName(dgraphTypes []interface{}) string {
	return (*field)(m).TypeName(dgraphTypes)
}

func (m *mutation) IncludeInterfaceField(dgraphTypes []interface{}) bool {
	return (*field)(m).IncludeInterfaceField(dgraphTypes)
}

func (t *astType) Field(name string) FieldDefinition {
	return &fieldDefinition{
		// this ForName lookup is a loop in the underlying schema :-(
		fieldDef:        t.inSchema.Types[t.Name()].Fields.ForName(name),
		inSchema:        t.inSchema,
		dgraphPredicate: t.dgraphPredicate,
	}
}

func (t *astType) Fields() []FieldDefinition {
	var result []FieldDefinition

	for _, fld := range t.inSchema.Types[t.Name()].Fields {
		result = append(result,
			&fieldDefinition{
				fieldDef:        fld,
				inSchema:        t.inSchema,
				dgraphPredicate: t.dgraphPredicate,
			})
	}

	return result
}

func (fd *fieldDefinition) Name() string {
	return fd.fieldDef.Name
}

func (fd *fieldDefinition) IsID() bool {
	return isID(fd.fieldDef)
}

func hasIDDirective(fd *ast.FieldDefinition) bool {
	id := fd.Directives.ForName("id")
	return id != nil
}

func isID(fd *ast.FieldDefinition) bool {
	return fd.Type.Name() == "ID"
}

func (fd *fieldDefinition) Type() Type {
	return &astType{
		typ:             fd.fieldDef.Type,
		inSchema:        fd.inSchema,
		dgraphPredicate: fd.dgraphPredicate,
	}
}

func (fd *fieldDefinition) Inverse() FieldDefinition {

	invDirective := fd.fieldDef.Directives.ForName(inverseDirective)
	if invDirective == nil {
		return nil
	}

	invFieldArg := invDirective.Arguments.ForName(inverseArg)
	if invFieldArg == nil {
		return nil // really not possible
	}

	// typ must exist if the schema passed GQL validation
	typ := fd.inSchema.Types[fd.Type().Name()]

	// fld must exist if the schema passed our validation
	fld := typ.Fields.ForName(invFieldArg.Value.Raw)

	return &fieldDefinition{
		fieldDef:        fld,
		inSchema:        fd.inSchema,
		dgraphPredicate: fd.dgraphPredicate}
}

// ForwardEdge gets the field definition for a forward edge if this field is a reverse edge
// i.e. if it has a dgraph directive like
// @dgraph(name: "~movies")
func (fd *fieldDefinition) ForwardEdge() FieldDefinition {
	dd := fd.fieldDef.Directives.ForName(dgraphDirective)
	if dd == nil {
		return nil
	}

	arg := dd.Arguments.ForName(dgraphPredArg)
	if arg == nil {
		return nil // really not possible
	}
	name := arg.Value.Raw

	if !strings.HasPrefix(name, "~") && !strings.HasPrefix(name, "<~") {
		return nil
	}

	fedge := strings.Trim(name, "<~>")
	// typ must exist if the schema passed GQL validation
	typ := fd.inSchema.Types[fd.Type().Name()]

	var fld *ast.FieldDefinition
	// Have to range through all the fields and find the correct forward edge. This would be
	// expensive and should ideally be cached on schema update.
	for _, field := range typ.Fields {
		dir := field.Directives.ForName(dgraphDirective)
		if dir == nil {
			continue
		}
		predArg := dir.Arguments.ForName(dgraphPredArg)
		if predArg == nil || predArg.Value.Raw == "" {
			continue
		}
		if predArg.Value.Raw == fedge {
			fld = field
			break
		}
	}

	return &fieldDefinition{
		fieldDef:        fld,
		inSchema:        fd.inSchema,
		dgraphPredicate: fd.dgraphPredicate}
}

func (t *astType) Name() string {
	if t.typ.NamedType == "" {
		return t.typ.Elem.NamedType
	}
	return t.typ.NamedType
}

func (t *astType) DgraphName() string {
	typeDef := t.inSchema.Types[t.typ.Name()]
	name := typeName(typeDef)
	if name != "" {
		return name
	}
	return t.Name()
}

func (t *astType) Nullable() bool {
	return !t.typ.NonNull
}

func (t *astType) ListType() Type {
	if t.typ.Elem == nil {
		return nil
	}
	return &astType{typ: t.typ.Elem}
}

// DgraphPredicate returns the name of the predicate in Dgraph that represents this
// type's field fld.  Mostly this will be type_name.field_name,.
func (t *astType) DgraphPredicate(fld string) string {
	return t.dgraphPredicate[t.Name()][fld]
}

func (t *astType) String() string {
	if t == nil {
		return ""
	}

	var sb strings.Builder
	// give it enough space in case it happens to be `[t.Name()!]!`
	sb.Grow(len(t.Name()) + 4)

	if t.ListType() == nil {
		x.Check2(sb.WriteString(t.Name()))
	} else {
		// There's no lists of lists, so this needn't be recursive
		x.Check2(sb.WriteRune('['))
		x.Check2(sb.WriteString(t.Name()))
		if !t.ListType().Nullable() {
			x.Check2(sb.WriteRune('!'))
		}
		x.Check2(sb.WriteRune(']'))
	}

	if !t.Nullable() {
		x.Check2(sb.WriteRune('!'))
	}

	return sb.String()
}

func (t *astType) IDField() FieldDefinition {
	def := t.inSchema.Types[t.Name()]
	if def.Kind != ast.Object && def.Kind != ast.Interface {
		return nil
	}

	for _, fd := range def.Fields {
		if isID(fd) {
			return &fieldDefinition{
				fieldDef: fd,
				inSchema: t.inSchema,
			}
		}
	}

	return nil
}

func (t *astType) PasswordField() FieldDefinition {
	def := t.inSchema.Types[t.Name()]
	if def.Kind != ast.Object && def.Kind != ast.Interface {
		return nil
	}

	fd := getPasswordField(def)
	if fd == nil {
		return nil
	}

	return &fieldDefinition{
		fieldDef: fd,
		inSchema: t.inSchema,
	}
}

func (t *astType) XIDField() FieldDefinition {
	def := t.inSchema.Types[t.Name()]
	if def.Kind != ast.Object && def.Kind != ast.Interface {
		return nil
	}

	for _, fd := range def.Fields {
		if hasIDDirective(fd) {
			return &fieldDefinition{
				fieldDef: fd,
				inSchema: t.inSchema,
			}
		}
	}

	return nil
}

func (t *astType) Interfaces() []string {
	interfaces := t.inSchema.Types[t.typ.Name()].Interfaces
	if len(interfaces) == 0 {
		return nil
	}

	// Look up the interface types in the schema and find their typeName which could have been
	// overwritten using @dgraph(type: ...)
	names := make([]string, 0, len(interfaces))
	for _, intr := range interfaces {
		i := t.inSchema.Types[intr]
		name := intr
		if n := typeName(i); n != "" {
			name = n
		}
		names = append(names, name)
	}
	return names
}

// CheckNonNulls checks that any non nullables in t are present in obj.
// Fields of type ID are not checked, nor is any exclusion.
//
// For our reference types for adding/linking objects, we'd like to have something like
//
// input PostRef {
// 	id: ID!
// }
//
// input PostNew {
// 	title: String!
// 	text: String
// 	author: AuthorRef!
// }
//
// and then have something like this
//
// input PostNewOrReference = PostRef | PostNew
//
// input AuthorNew {
//   ...
//   posts: [PostNewOrReference]
// }
//
// but GraphQL doesn't allow union types in input, so best we can do is
//
// input PostRef {
// 	id: ID
// 	title: String
// 	text: String
// 	author: AuthorRef
// }
//
// and then check ourselves that either there's an ID, or there's all the bits to
// satisfy a valid post.
func (t *astType) EnsureNonNulls(obj map[string]interface{}, exclusion string) error {
	for _, fld := range t.inSchema.Types[t.Name()].Fields {
		if fld.Type.NonNull && !isID(fld) && !(fld.Name == exclusion) {
			if val, ok := obj[fld.Name]; !ok || val == nil {
				return errors.Errorf(
					"type %s requires a value for field %s, but no value present",
					t.Name(), fld.Name)
			}
		}
	}
	return nil
}
