package runtime

import (
	"encoding/binary"
	"errors"
	log "github.com/ChainSafe/log15"
	wasm "github.com/wasmerio/go-ext-wasm/wasmer"
	"math/bits"
)

// This module implements a freeing-bump allocator
// see more details at https://github.com/paritytech/substrate/issues/1615

// The pointers need to be aligned to 8 bytes
const ALIGNMENT uint32 = 8
const N = 22
const MAX_POSSIBLE_ALLOCATION = 16777216 // 2^24 bytes

type FreeingBumpHeapAllocator struct {
	bumper        uint32
	heads         [N]uint32
	heap          *wasm.Memory
	max_heap_size uint32
	ptr_offset    uint32
	total_size    uint32
}

// Creates a new allocation heap which follows a freeing-bump strategy.
// The maximum size which can be allocated at once is 16 MiB.
//
// # Arguments
//
// * `mem` - A `MemoryRef` to the available `MemoryInstance` which is
//   used as the heap.
//
// * `ptr_offset` - The pointers returned by `allocate()` start from this
//   offset on. The pointer offset needs to be aligned to a multiple of 8,
//   hence a padding might be added to align `ptr_offset` properly.
//
// * returns an initilized FreeingBumpHeapAllocator
func newAllocator(mem *wasm.Memory, ptr_offset uint32) FreeingBumpHeapAllocator {
	fbha := new(FreeingBumpHeapAllocator)
	current_size := mem.Length()
	// we don't include offset memory in the heap
	heap_size := uint32(current_size) - ptr_offset

	padding := ptr_offset % ALIGNMENT
	if padding != 0 {
		ptr_offset += ALIGNMENT - padding
	}

	fbha.bumper = 0
	fbha.heap = mem
	fbha.max_heap_size = heap_size
	fbha.ptr_offset = ptr_offset
	fbha.total_size = 0

	return *fbha
}

func (fbha *FreeingBumpHeapAllocator) allocate(size uint32) (uint32, error) {
	// test for space allocation
	if size > MAX_POSSIBLE_ALLOCATION {
		err := errors.New("Error: size to large")
		return 0, err
	}
	item_size := nextPowerOf2GT8(size)

	if (item_size + 8 + fbha.total_size) > fbha.max_heap_size {
		err := errors.New("Error: allocator out of space")
		return 0, err
	}

	// get pointer based on list_index
	list_index := bits.TrailingZeros32(item_size) - 3

	var ptr uint32
	if fbha.heads[list_index] != 0 {
		// Something from the free list
		item := fbha.heads[list_index]
		four_bytes := fbha.get_heap_4bytes(item)
		fbha.heads[list_index] = binary.LittleEndian.Uint32(four_bytes)
		ptr = item + 8
	} else {
		// Nothing te be freed. Bump.
		ptr = fbha.bump(item_size+8) + 8
	}

	// write "header" for allocated memory to heap
	for i := uint32(1); i <= 8; i++ {
		fbha.set_heap(ptr-i, 255)
	}
	fbha.set_heap(ptr-8, uint8(list_index))
	fbha.total_size = fbha.total_size + item_size + 8
	log.Debug("[allocate]", "heap_size after allocation", fbha.total_size)
	return fbha.ptr_offset + ptr, nil
}

func (fbha *FreeingBumpHeapAllocator) deallocate(pointer uint32) error {
	ptr := pointer - fbha.ptr_offset
	if ptr < 8 {
		return errors.New("Invalid pointer for deallocation")
	}
	log.Debug("[deallocate]", "ptr", ptr)
	list_index := fbha.get_heap_byte(ptr - 8)

	// update heads array, and heap "header"
	tail := fbha.heads[list_index]
	fbha.heads[list_index] = ptr - 8

	bTail := make([]byte, 4)
	binary.LittleEndian.PutUint32(bTail, tail)
	fbha.set_heap_4bytes(ptr-8, bTail)

	// update heap total size
	item_size := get_item_size_from_index(uint(list_index))
	fbha.total_size = fbha.total_size - uint32(item_size+8)
	log.Debug("[deallocate]", "heap total_size after deallocate", fbha.total_size)

	return nil
}

func (fbha *FreeingBumpHeapAllocator) bump(n uint32) uint32 {
	res := fbha.bumper
	fbha.bumper += n
	return res
}

func (fbha *FreeingBumpHeapAllocator) set_heap(ptr uint32, value uint8) {
	fbha.heap.Data()[fbha.ptr_offset+ptr] = value
}

func (fbha *FreeingBumpHeapAllocator) set_heap_4bytes(ptr uint32, value []byte) {
	copy(fbha.heap.Data()[fbha.ptr_offset+ptr:fbha.ptr_offset+ptr+4], value)
}
func (fbha *FreeingBumpHeapAllocator) get_heap_4bytes(ptr uint32) []byte {
	return fbha.heap.Data()[fbha.ptr_offset+ptr : fbha.ptr_offset+ptr+4]
}

func (fbha *FreeingBumpHeapAllocator) get_heap_byte(ptr uint32) byte {
	return fbha.heap.Data()[fbha.ptr_offset+ptr]
}

func get_item_size_from_index(index uint) uint {
	// we shift 1 by three places since the first possible item size is 8
	return 1 << 3 << index
}

func nextPowerOf2GT8(v uint32) uint32 {
	if v < 8 {
		return 8
	}
	v--
	v |= v >> 1
	v |= v >> 2
	v |= v >> 4
	v |= v >> 8
	v |= v >> 16
	v++
	return v
}
