// Copyright (c) 2012-2019 Eli Janssen
// Use of this source code is governed by an MIT-style
// license that can be found in the LICENSE file.

package htrie

import (
	"fmt"
	"strings"
)

const globChar uint8 = 1

type BitMask uint8

const (
	isGlob BitMask = 1 << iota
	canMatch
	hasGlobChild
	oneShot
)

func (m BitMask) String() string {
	if m >= oneShot {
		return fmt.Sprintf("<unknown key: %d>", m)
	}

	switch m {
	case isGlob:
		return "glob"
	case canMatch:
		return "can-match"
	case hasGlobChild:
		return "glob-child"
	case oneShot:
		return "one-shot"
	}

	// multiple keys
	var names []string
	for key := isGlob; key < oneShot; key <<= 1 {
		if m&key != 0 {
			names = append(names, key.String())
		}
	}
	return strings.Join(names, "|")
}

// A globPathNode represents a path checker that supports globbing comparisons
type globPathNode struct {
	nodeChars []uint8
	nodeAttrs []BitMask
	nodeTree  [][]int
	icase     bool
}

func (gpn *globPathNode) addPath(s string) error {
	if gpn == nil {
		return fmt.Errorf("got nil <gpn> in receiver")
	}

	mlen := len(s)
	prevnode := 0
	curnode := 0
	nextnode := 0
	//for _, part := range s {
	for i := 0; i < mlen; i++ {
		part := uint8(s[i])

		// if icase, use lowercase letters for comparisons
		// 'A' == 65; 'Z' == 90
		if gpn.icase && 65 <= part && part <= 90 {
			part = part + 32
		}

		var c uint8
		// '*' == 42
		if part == 42 {
			c = globChar
		} else {
			c = part
		}

		// subt[c] == nil
		found := false
		for subTreeIndex := range gpn.nodeTree[curnode] {
			idx := gpn.nodeTree[curnode][subTreeIndex]
			if gpn.nodeChars[idx] == c {
				nextnode = int(idx)
				found = true
				break
			}
		}
		if !found {
			gpn.nodeTree = append(gpn.nodeTree, make([]int, 0))
			gpn.nodeAttrs = append(gpn.nodeAttrs, 0)
			gpn.nodeChars = append(gpn.nodeChars, c)
			newIdx := len(gpn.nodeChars) - 1
			gpn.nodeTree[curnode] = append(gpn.nodeTree[curnode], newIdx)
			nextnode = newIdx
		}

		// setup oneshot as an optimizaiton if there is only one subcandidate...
		if len(gpn.nodeTree[curnode]) == 1 {
			gpn.nodeAttrs[curnode] |= oneShot
		} else {
			gpn.nodeAttrs[curnode] &^= oneShot
		}

		prevnode = curnode
		curnode = nextnode
		if c == globChar {
			gpn.nodeAttrs[prevnode] |= hasGlobChild
			gpn.nodeAttrs[curnode] |= isGlob
		}
	}

	// this is the end of the path, so this node can be a match, even if future
	// urls add children to it (a longer url).
	gpn.nodeAttrs[curnode] |= canMatch
	return nil
}

func (gpn *globPathNode) globConsume(s string, index, mlen, nodeIndex int) bool {
	curnode := nodeIndex

	// we have a glob and no follow-on chars, so we can consume
	// till the end and then match. early return
	if gpn.nodeAttrs[curnode]&canMatch != 0 {
		return true
	}

	// otherwise we have some work to do...
	// don't need to iter runes since we have ascii
	for i := index; i < mlen; i++ {
		part := uint8(s[i])

		// if icase, use lowercase letters for comparisons
		// 'A' == 65; 'Z' == 90
		if gpn.icase && 65 <= part && part <= 90 {
			part = part + 32
		}

		x := gpn.nodeChars[curnode]
		if x == globChar {
			x = '*'
		}
		nextX := gpn.nodeChars[gpn.nodeTree[curnode][0]]
		if nextX == globChar {
			nextX = '*'
		}

		// optimize common single char after * globbing
		// eg. .../*/...
		// if we know the glob has one one subcandidate (next char), we consume until
		// we hit one of those
		if gpn.nodeAttrs[curnode]&oneShot != 0 && len(gpn.nodeTree[curnode]) > 0 {
			idx := gpn.nodeTree[curnode][0]
			if part != gpn.nodeChars[idx] {
				continue
			}
		}

		for j := range gpn.nodeTree[curnode] {
			idx := gpn.nodeTree[curnode][j]
			if gpn.nodeChars[idx] == part {
				// found a candidate. follow it with normal branch logic.
				// if it matches, we're done!
				// increment index value for checkPath because we consumed a char
				// by following oneShot
				if gpn.checkPath(s, i+1, mlen, idx) {
					return true
				}
			}
		}

		// was this the last char in path?
		if i == mlen-1 {
			// reached the end without a match, and the glob wasn't at the end
			// of the line... return whether the curnode can match or not,
			// to determine overall success or failure
			return gpn.nodeAttrs[curnode]&canMatch != 0
		}
	}

	// exhausted the string, but never found a match
	return false
}

func (gpn *globPathNode) checkPath(s string, index, mlen int, nodeIndex int) bool {
	curnode := nodeIndex
	// don't need to iter runes since we have ascii
	for i := index; i < mlen; i++ {
		part := uint8(s[i])

		// if icase, use lowercase letters for comparisons
		// 'A' == 65; 'Z' == 90
		if gpn.icase && 65 <= part && part <= 90 {
			part = part + 32
		}

		// node may have a glob child candidate (consumes), check that first
		if gpn.nodeAttrs[curnode]&hasGlobChild != 0 {
			// get glob node, and call globconsume.
			// don't advance string pointer here though, as a glob is a match
			// node and not a standard char node (it can also match zero characters)
			/// find glob child
			for j := range gpn.nodeTree[curnode] {
				idx := gpn.nodeTree[curnode][j]
				if gpn.nodeChars[idx] == globChar {
					// found our node
					if gpn.globConsume(s, i, mlen, idx) {
						return true
					}
				}
			}
		}

		// oneshot means we only have one child candidate -- an optimization (fastpath)
		// to avoid the slow path map fallback
		if gpn.nodeAttrs[curnode]&oneShot != 0 {
			// only one candidate, and it _was_ the glob we tried.
			// we're done!
			idx := gpn.nodeTree[curnode][0]
			if gpn.nodeChars[idx] == globChar { // or gpn.nodeAttrs[idx][0] (isGlob)
				return false
			}

			// if oneshot matches, use it
			if gpn.nodeChars[idx] == part {
				curnode = idx
				continue
			}

			// we had once chance, and it wasn't a glob or a match
			// work is done on this branch
			return false
		}

		// more than one candidate, so fallback to map lookup, since we don't
		// know anything else
		found := false
		for j := range gpn.nodeTree[curnode] {
			idx := gpn.nodeTree[curnode][j]
			if gpn.nodeChars[idx] == part {
				curnode = idx
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// reached the end of the string.. check if curnode is a leaf or globby
	// note: unlikely we would end up with a globby here, but maybe possible.
	return gpn.nodeAttrs[curnode]&(isGlob|canMatch) != 0
}

func newGlobPathNode(icase bool) *globPathNode {
	// refs for valid tree chars
	// https://www.w3.org/TR/2011/WD-html5-20110525/urls.html (refers to RFC 3986)
	// https://en.wikipedia.org/wiki/Uniform_Resource_Identifier#Generic_syntax
	// http://www.asciitable.com
	//
	// omit: less than or equal to 0x0020 or greater than or equal to 0x007F
	// omit: 0x0022, 0x003C, 0x003E, 0x005B, 0x005E, 0x0060, and 0x007B to 0x007D
	// ... so set is:
	//   0x0021             33
	//   0x0023...0x003B    35-59
	//   0x003D             61
	//   0x003F...0x005A    63-90
	//   0x005C             92
	//   0x005D             93
	//   0x005F             94
	//   0x0061...0x007A    97-122
	//   0x007E             126
	// so a total possible of 85 chars, but spread out over 94 slots
	// since there are quite a few possible slots, let's use a map for now...
	// web searches say a map is faster in go above a certain size. benchmark later...

	// for now, since realloc cost is paid at creation, and we want to RSS size
	// and since we only /really/ care about lookup costs, just start with 0 initial
	// map size and let it grow as needed
	return &globPathNode{
		nodeChars: []uint8{0},
		nodeTree:  [][]int{{}},
		nodeAttrs: []BitMask{0},
		icase:     icase,
	}
}
