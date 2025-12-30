# Warp Store JS

A high-performance, dual-layer caching library designed for modern web applications. `warp-store` combines the speed of in-memory caching with the persistence of IndexedDB to create a resilient, offline-ready data store.

**Framework Agnostic**: Works with Vanilla JS, Vue, React, Svelte, Angular, etc.

## Features

- **Dual-Layer Caching (L1 + L2)**:
  - **L1 (Memory)**: Instant access using standard `Map`.
  - **L2 (Disk)**: Persistent storage using `IndexedDB`.
- **Reactivity**: Built-in subscription system (`subscribe`).
- **Lease-Based Session Management**: Associates data with a specific session.
- **TTL Support**: Built-in Time-To-Live.
- **Zero Dependencies**: Lightweight and fast.

## Installation

```json
{
  "dependencies": {
    "warp-store": "file:../warp-store"
  }
}
```

## Usage

### 1. Basic (Vanilla JS)

```javascript
import { warpStore } from 'warp-store';

// Subscribe to changes
const unsubscribe = warpStore.subscribe((event) => {
    console.log('Store changed:', event);
    // event: { type: 'set', key: '...' } | { type: 'purge' }
});

// Init
await warpStore.restoreSession();

// Set
await warpStore.set('user:profile', { name: 'Mirko' });

// Get
const profile = await warpStore.get('user:profile');
```

### 2. Vue Integration

Since `warp-store` is plain JS, you can make it reactive easily.

```javascript
// store.js
import { reactive } from 'vue';
import { warpStore } from 'warp-store';

// Create a reactive wrapper if you need deep watching, 
// OR just trigger updates via subscribe.

const state = reactive({
  data: new Map()
});

warpStore.subscribe(({ type, key, value }) => {
  if (type === 'set') state.data.set(key, value);
  if (type === 'delete') state.data.delete(key);
  if (type === 'purge') state.data.clear();
});

export const useWarpStore = () => {
    return {
        get: warpStore.get.bind(warpStore),
        set: warpStore.set.bind(warpStore),
        state // Reactive state for template
    }
};
```

### 3. Svelte Integration

Everything in Svelte is a store.

```javascript
// store.js
import { writable } from 'svelte/store';
import { warpStore } from 'warp-store';

function createWarpStore() {
    const { subscribe, set, update } = writable(new Map());

    // Sync from internal WarpStore to Svelte Store
    warpStore.subscribe(({ type, key, value }) => {
        update(map => {
            if (type === 'set') map.set(key, value);
            else if (type === 'delete') map.delete(key);
            else if (type === 'purge') map.clear();
            return map; // Trigger update
        });
    });

    return {
        subscribe,
        get: (key) => warpStore.get(key),
        set: (key, val) => warpStore.set(key, val),
        init: () => warpStore.restoreSession()
    };
}

export const store = createWarpStore();
```

## Architecture

1. **L1 Cache (Memory)**: Uses `Map`. Extremely fast.
2. **L2 Cache (IndexedDB)**: Async persistence.
3. **Bus**: Internal event emitter that notifies subscribers of changes.

## API

- `get(key)`: Promise<val>
- `set(key, val, ttl)`: Promise<void>
- `subscribe(cb)`: UnsubscribeFn
- `purge()`: Promise<void>
