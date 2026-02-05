import { useState } from 'react';

// Persona definitions matching config/prompts.json
const PERSONAS = {
  assistant: {
    name: "Helpful Assistant",
    description: "A friendly, general-purpose voice assistant",
    voiceName: "Rachel",
    icon: "🤖",
  },
  technical: {
    name: "Technical Expert",
    description: "A knowledgeable tech support specialist",
    voiceName: "Antoni",
    icon: "💻",
  },
  creative: {
    name: "Creative Partner",
    description: "A brainstorming companion for ideas",
    voiceName: "Bella",
    icon: "🎨",
  },
  coach: {
    name: "Life Coach",
    description: "A supportive personal coach and mentor",
    voiceName: "Elli",
    icon: "🎯",
  },
  tutor: {
    name: "Patient Tutor",
    description: "An educational guide that adapts to learning styles",
    voiceName: "Josh",
    icon: "📚",
  },
  interviewer: {
    name: "Interview Coach",
    description: "Practice interviews and get feedback",
    voiceName: "Arnold",
    icon: "💼",
  },
  storyteller: {
    name: "Storyteller",
    description: "An engaging narrator for interactive stories",
    voiceName: "Adam",
    icon: "📖",
  },
  debug: {
    name: "Debug Partner",
    description: "Pair programming and debugging assistant",
    voiceName: "Sam",
    icon: "🔧",
  },
  meditation: {
    name: "Mindfulness Guide",
    description: "Calm, grounding voice for relaxation",
    voiceName: "Dorothy",
    icon: "🧘",
  },
  language: {
    name: "Language Partner",
    description: "Practice conversations in different languages",
    voiceName: "Michael",
    icon: "🌍",
  },
} as const;

type PersonaKey = keyof typeof PERSONAS;

interface PersonaSelectorProps {
  selectedPersona: PersonaKey;
  onSelectPersona: (persona: PersonaKey) => void;
  disabled?: boolean;
}

export function PersonaSelector({ selectedPersona, onSelectPersona, disabled }: PersonaSelectorProps) {
  const [isExpanded, setIsExpanded] = useState(false);

  const currentPersona = PERSONAS[selectedPersona];

  return (
    <div style={styles.container}>
      <h2 style={styles.title}>AI Persona</h2>

      {/* Current selection display */}
      <button
        style={{
          ...styles.selectedButton,
          opacity: disabled ? 0.6 : 1,
          cursor: disabled ? 'not-allowed' : 'pointer',
        }}
        onClick={() => !disabled && setIsExpanded(!isExpanded)}
        disabled={disabled}
      >
        <span style={styles.icon}>{currentPersona.icon}</span>
        <div style={styles.selectedInfo}>
          <div style={styles.selectedName}>{currentPersona.name}</div>
          <div style={styles.selectedVoice}>Voice: {currentPersona.voiceName}</div>
        </div>
        <span style={styles.chevron}>{isExpanded ? '▲' : '▼'}</span>
      </button>

      {/* Dropdown list */}
      {isExpanded && !disabled && (
        <div style={styles.dropdown}>
          {(Object.entries(PERSONAS) as [PersonaKey, typeof PERSONAS[PersonaKey]][]).map(([key, persona]) => (
            <button
              key={key}
              style={{
                ...styles.option,
                backgroundColor: key === selectedPersona ? '#e0e7ff' : 'transparent',
              }}
              onClick={() => {
                onSelectPersona(key);
                setIsExpanded(false);
              }}
            >
              <span style={styles.optionIcon}>{persona.icon}</span>
              <div style={styles.optionInfo}>
                <div style={styles.optionName}>{persona.name}</div>
                <div style={styles.optionDesc}>{persona.description}</div>
              </div>
              {key === selectedPersona && <span style={styles.checkmark}>✓</span>}
            </button>
          ))}
        </div>
      )}

      {/* CLI command hint */}
      <div style={styles.hint}>
        <span style={styles.hintLabel}>Agent command:</span>
        <code style={styles.hintCode}>
          go run examples/ai_agent/main.go -id ai-agent -persona {selectedPersona}
        </code>
      </div>
    </div>
  );
}

const styles: Record<string, React.CSSProperties> = {
  container: {
    marginBottom: '16px',
  },
  title: {
    margin: '0 0 12px 0',
    fontSize: '18px',
    color: '#374151',
  },
  selectedButton: {
    width: '100%',
    display: 'flex',
    alignItems: 'center',
    gap: '12px',
    padding: '12px 16px',
    backgroundColor: '#f9fafb',
    border: '2px solid #e5e7eb',
    borderRadius: '8px',
    cursor: 'pointer',
    textAlign: 'left',
  },
  icon: {
    fontSize: '24px',
  },
  selectedInfo: {
    flex: 1,
  },
  selectedName: {
    fontWeight: '600',
    color: '#1f2937',
    fontSize: '16px',
  },
  selectedVoice: {
    fontSize: '12px',
    color: '#6b7280',
    marginTop: '2px',
  },
  chevron: {
    color: '#6b7280',
    fontSize: '12px',
  },
  dropdown: {
    marginTop: '8px',
    backgroundColor: '#fff',
    border: '1px solid #e5e7eb',
    borderRadius: '8px',
    boxShadow: '0 4px 6px rgba(0, 0, 0, 0.1)',
    maxHeight: '300px',
    overflowY: 'auto',
  },
  option: {
    width: '100%',
    display: 'flex',
    alignItems: 'center',
    gap: '12px',
    padding: '10px 14px',
    border: 'none',
    borderBottom: '1px solid #f3f4f6',
    cursor: 'pointer',
    textAlign: 'left',
    transition: 'background-color 0.15s',
  },
  optionIcon: {
    fontSize: '20px',
    width: '28px',
    textAlign: 'center',
  },
  optionInfo: {
    flex: 1,
  },
  optionName: {
    fontWeight: '500',
    color: '#1f2937',
    fontSize: '14px',
  },
  optionDesc: {
    fontSize: '12px',
    color: '#6b7280',
    marginTop: '2px',
  },
  checkmark: {
    color: '#4f46e5',
    fontWeight: 'bold',
  },
  hint: {
    marginTop: '12px',
    padding: '10px',
    backgroundColor: '#f3f4f6',
    borderRadius: '6px',
    fontSize: '12px',
  },
  hintLabel: {
    color: '#6b7280',
    display: 'block',
    marginBottom: '4px',
  },
  hintCode: {
    display: 'block',
    fontFamily: 'monospace',
    fontSize: '11px',
    color: '#1f2937',
    wordBreak: 'break-all',
  },
};

export type { PersonaKey };
export { PERSONAS };
