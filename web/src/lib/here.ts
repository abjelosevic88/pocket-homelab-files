// The folder on screen, published by the browser page so the sidebar's New
// menu, the palette and the shortcuts act on it (and on My files elsewhere).
export interface Here { space: string; path: string; writable: boolean; name: string }

const HOME: Here = { space: 'home', path: '/', writable: true, name: 'My files' }
let here: Here | null = null

export function setHere(h: Here | null) { here = h }
/** Where new files and folders go: the open folder if it takes them, else My files. */
export function uploadTarget(): Here { return here && here.writable ? here : HOME }
export function currentFolder(): Here | null { return here }
