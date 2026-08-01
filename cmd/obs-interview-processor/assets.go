package main

import _ "embed"

//go:embed obs_interview_hook.lua
var luaHookTemplate string

//go:embed obs_interview_notifier.swift
var notifierSwiftSource string

//go:embed obs_interview_prompt.swift
var promptSwiftSource string
