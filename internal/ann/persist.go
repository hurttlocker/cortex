package ann

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
)

// File format: cortex-hnsw v1
// Header: magic(8) + version(4) + dims(4) + nodeCount(4) + entryPoint(4) + maxLevel(4) + M(4) + Mmax0(4) + efConst(4) + efSearch(4)
// Per node: id(8) + level(4) + vector(dims*4) + for each layer: friendCount(4) + friends(friendCount*4)

const magic = "CXHNSW01"

// Save persists the HNSW index to a binary file.
// The index can be loaded back with Load().
func (idx *Index) Save(path string) error {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("creating index file: %w", err)
	}
	defer f.Close()

	w := &countWriter{w: f}

	// Header
	if _, err := w.Write([]byte(magic)); err != nil {
		return err
	}
	if err := writeInt32(w, 1); err != nil { // version
		return err
	}
	if err := writeInt32(w, int32(idx.dims)); err != nil {
		return err
	}
	if err := writeInt32(w, int32(len(idx.nodes))); err != nil {
		return err
	}
	if err := writeInt32(w, int32(idx.entryPoint)); err != nil {
		return err
	}
	if err := writeInt32(w, int32(idx.maxLevel)); err != nil {
		return err
	}
	if err := writeInt32(w, int32(idx.M)); err != nil {
		return err
	}
	if err := writeInt32(w, int32(idx.Mmax0)); err != nil {
		return err
	}
	if err := writeInt32(w, int32(idx.EfConstruction)); err != nil {
		return err
	}
	if err := writeInt32(w, int32(idx.EfSearch)); err != nil {
		return err
	}

	// Nodes
	for _, n := range idx.nodes {
		// ID (int64)
		if err := writeInt64(w, n.id); err != nil {
			return err
		}
		// Level
		if err := writeInt32(w, int32(n.level)); err != nil {
			return err
		}
		// Vector
		for _, v := range n.vector {
			if err := writeFloat32(w, v); err != nil {
				return err
			}
		}
		// Friends per layer
		for l := 0; l <= n.level; l++ {
			friends := n.friends[l]
			if err := writeInt32(w, int32(len(friends))); err != nil {
				return err
			}
			for _, f := range friends {
				if err := writeInt32(w, int32(f)); err != nil {
					return err
				}
			}
		}
	}

	return f.Sync()
}

// Load restores an HNSW index from a binary file created by Save().
func Load(path string) (*Index, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening index file: %w", err)
	}
	defer f.Close()

	// Read magic
	magicBuf := make([]byte, 8)
	if _, err := io.ReadFull(f, magicBuf); err != nil {
		return nil, fmt.Errorf("reading magic: %w", err)
	}
	if string(magicBuf) != magic {
		return nil, fmt.Errorf("invalid magic: %q (expected %q)", string(magicBuf), magic)
	}

	// Read header
	version, err := readInt32(f)
	if err != nil {
		return nil, fmt.Errorf("reading version: %w", err)
	}
	if version != 1 {
		return nil, fmt.Errorf("unsupported version: %d", version)
	}

	dims, err := readInt32(f)
	if err != nil {
		return nil, err
	}
	nodeCount, err := readInt32(f)
	if err != nil {
		return nil, err
	}
	entryPoint, err := readInt32(f)
	if err != nil {
		return nil, err
	}
	maxLevel, err := readInt32(f)
	if err != nil {
		return nil, err
	}
	m, err := readInt32(f)
	if err != nil {
		return nil, err
	}
	mmax0, err := readInt32(f)
	if err != nil {
		return nil, err
	}
	efConst, err := readInt32(f)
	if err != nil {
		return nil, err
	}
	efSearch, err := readInt32(f)
	if err != nil {
		return nil, err
	}

	idx := &Index{
		dims:           int(dims),
		M:              int(m),
		Mmax0:          int(mmax0),
		EfConstruction: int(efConst),
		EfSearch:       int(efSearch),
		LevelMult:      1.0 / math.Log(float64(m)),
		entryPoint:     int(entryPoint),
		maxLevel:       int(maxLevel),
		nodes:          make([]node, 0, nodeCount),
		idToIdx:        make(map[int64]int, nodeCount),
	}

	// Read nodes
	for i := int32(0); i < nodeCount; i++ {
		id, err := readInt64(f)
		if err != nil {
			return nil, fmt.Errorf("reading node %d id: %w", i, err)
		}
		level, err := readInt32(f)
		if err != nil {
			return nil, fmt.Errorf("reading node %d level: %w", i, err)
		}

		// Vector
		vector := make([]float32, dims)
		for d := int32(0); d < dims; d++ {
			v, err := readFloat32(f)
			if err != nil {
				return nil, fmt.Errorf("reading node %d vector[%d]: %w", i, d, err)
			}
			vector[d] = v
		}

		// Friends
		friends := make([][]int, level+1)
		for l := int32(0); l <= level; l++ {
			friendCount, err := readInt32(f)
			if err != nil {
				return nil, fmt.Errorf("reading node %d layer %d friend count: %w", i, l, err)
			}
			friends[l] = make([]int, friendCount)
			for j := int32(0); j < friendCount; j++ {
				fIdx, err := readInt32(f)
				if err != nil {
					return nil, fmt.Errorf("reading node %d layer %d friend %d: %w", i, l, j, err)
				}
				friends[l][j] = int(fIdx)
			}
		}

		n := node{
			id:      id,
			vector:  vector,
			friends: friends,
			level:   int(level),
		}
		idx.nodes = append(idx.nodes, n)
		idx.idToIdx[id] = int(i)
	}

	return idx, nil
}

// Binary helpers

type countWriter struct {
	w io.Writer
}

func (cw *countWriter) Write(p []byte) (int, error) {
	return cw.w.Write(p)
}

func writeInt32(w io.Writer, v int32) error {
	return binary.Write(w, binary.LittleEndian, v)
}

func writeInt64(w io.Writer, v int64) error {
	return binary.Write(w, binary.LittleEndian, v)
}

func writeFloat32(w io.Writer, v float32) error {
	return binary.Write(w, binary.LittleEndian, v)
}

func readInt32(r io.Reader) (int32, error) {
	var v int32
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func readInt64(r io.Reader) (int64, error) {
	var v int64
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}

func readFloat32(r io.Reader) (float32, error) {
	var v float32
	err := binary.Read(r, binary.LittleEndian, &v)
	return v, err
}
