import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Go to', keys: [[['h'], 'My files'], [['s'], 'Starred'], [['p'], 'Shared'], [['l'], 'Links'], [['i'], 'Upload links'], [['t'], 'Trash'], [[','], 'Settings'], [['/'], 'Search & commands'], [['⌘', 'K'], 'Search & commands']] },
  { title: 'New', keys: [[['u'], 'Upload files'], [['n'], 'New folder'], [['l'], 'Send files with a link']] },
  { title: 'In a folder', keys: [[['⌘', 'A'], 'Select everything'], [['Del'], 'Delete the selection'], [['F2'], 'Rename'], [['esc'], 'Clear the selection'], [['←', '→'], 'Previous / next file in the preview']] },
  { title: 'Window', keys: [[['['], 'Show or hide the sidebar'], [['esc'], 'Close a dialog or menu'], [['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
      <p className="muted small mt">New files and folders go into the folder you have open, or into My files.</p>
    </Modal>
  )
}
