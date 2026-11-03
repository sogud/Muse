import { describe, expect, it } from 'vitest'
import type { StoryContent } from '../src/index'
import { createInitialState, runStoryEvent } from '../src/index'

const sampleContent: StoryContent = {
  story: {
    storyId: 'midnight-cafe',
    startEpisodeId: 'ep_001',
    characters: ['luna', 'player'],
    variables: {
      lunaTrust: 0,
      playerEnergy: 50
    },
    episodes: [
      {
        id: 'ep_001',
        titleKey: 'ep_001.title',
        sceneIds: ['scene_001']
      }
    ]
  },
  episodes: [
    {
      episodeId: 'ep_001',
      titleKey: 'ep_001.title',
      summaryKey: 'ep_001.summary',
      targetExpressionIds: [],
      scenes: [
        {
          id: 'scene_001',
          background: 'bg_cafe_rainy_night',
          bgm: 'bgm_soft_rain',
          startNodeId: 'n001',
          nodes: [
            {
              id: 'n001',
              type: 'dialogue',
              speaker: 'luna',
              textKey: 'line.ep001.n001',
              sprite: 'luna.neutral',
              learningPointIds: ['expr_you_look_tired_today'],
              next: 'n002'
            },
            {
              id: 'n002',
              type: 'choice',
              promptKey: 'choice.ep001.n002.prompt',
              choices: [
                {
                  id: 'c1',
                  textKey: 'choice.ep001.n002.c1',
                  level: 'easy',
                  next: 'n003',
                  effects: {
                    lunaTrust: 1
                  }
                },
                {
                  id: 'c2',
                  textKey: 'choice.ep001.n002.c2',
                  level: 'natural',
                  next: 'n004',
                  effects: {
                    lunaTrust: 2
                  }
                }
              ]
            },
            {
              id: 'n003',
              type: 'dialogue',
              speaker: 'player',
              textKey: 'line.ep001.n003',
              learningPointIds: ['expr_im_tired'],
              next: 'n005'
            },
            {
              id: 'n004',
              type: 'dialogue',
              speaker: 'player',
              textKey: 'line.ep001.n004',
              learningPointIds: ['expr_i_had_a_long_day'],
              next: 'n005'
            },
            {
              id: 'n005',
              type: 'conditional',
              conditions: [
                {
                  variable: 'lunaTrust',
                  operator: 'gte',
                  value: 2,
                  next: 'n006'
                }
              ],
              defaultNext: 'n007'
            },
            {
              id: 'n006',
              type: 'dialogue',
              speaker: 'luna',
              textKey: 'line.ep001.n006',
              learningPointIds: ['expr_do_you_want_to_talk'],
              next: 'n008'
            },
            {
              id: 'n007',
              type: 'dialogue',
              speaker: 'luna',
              textKey: 'line.ep001.n007',
              learningPointIds: [],
              next: 'n008'
            },
            {
              id: 'n008',
              type: 'end',
              endingKey: 'ep_001.ending'
            }
          ]
        }
      ]
    }
  ],
  targetLocale: {
    locale: 'en-US',
    entries: {
      'ep_001.title': 'A Rainy Night',
      'ep_001.summary': 'You enter a quiet cafe after a long day.',
      'line.ep001.n001': 'You look tired today.',
      'choice.ep001.n002.prompt': 'How do you reply?',
      'choice.ep001.n002.c1': "I'm tired.",
      'choice.ep001.n002.c2': 'I had a long day.',
      'line.ep001.n003': "I'm tired.",
      'line.ep001.n004': 'I had a long day.',
      'line.ep001.n006': 'Do you want to talk about it?',
      'line.ep001.n007': 'Please rest for a moment.',
      'ep_001.ending': 'Episode end.'
    }
  },
  nativeLocale: {
    locale: 'zh-CN',
    entries: {
      'line.ep001.n001': '你今天看起来很累。',
      'choice.ep001.n002.prompt': '你要怎么回答？',
      'choice.ep001.n002.c1': '我累了。',
      'choice.ep001.n002.c2': '我今天过得很累。',
      'line.ep001.n006': '你想聊聊吗？'
    }
  }
}

describe('story-engine', () => {
  it('starts at the first renderable dialogue node', () => {
    const result = runStoryEvent(sampleContent, undefined, {
      type: 'START',
      storyId: 'midnight-cafe'
    })

    expect(result.state.nodeId).toBe('n001')
    expect(result.renderModel.dialogue?.text).toBe('You look tired today.')
    expect(result.renderModel.dialogue?.translation).toBe('你今天看起来很累。')
    expect(result.learningEvents).toEqual([
      {
        type: 'SEEN',
        itemId: 'expr_you_look_tired_today',
        nodeId: 'n001'
      }
    ])
  })

  it('advances from dialogue to choices', () => {
    const state = createInitialState(sampleContent)
    const result = runStoryEvent(sampleContent, state, { type: 'NEXT' })

    expect(result.state.nodeId).toBe('n002')
    expect(result.renderModel.choices).toHaveLength(2)
    expect(result.renderModel.choices?.[1]?.text).toBe('I had a long day.')
  })

  it('applies choice effects and follows conditional nodes', () => {
    const atChoice = runStoryEvent(sampleContent, createInitialState(sampleContent), { type: 'NEXT' })
    const afterChoice = runStoryEvent(sampleContent, atChoice.state, {
      type: 'CHOOSE',
      choiceId: 'c2'
    })

    expect(afterChoice.state.variables.lunaTrust).toBe(2)
    expect(afterChoice.state.nodeId).toBe('n004')

    const afterNext = runStoryEvent(sampleContent, afterChoice.state, { type: 'NEXT' })

    expect(afterNext.state.nodeId).toBe('n006')
    expect(afterNext.renderModel.dialogue?.text).toBe('Do you want to talk about it?')
  })

  it('renders the end node', () => {
    const atEnd = runStoryEvent(
      sampleContent,
      {
        ...createInitialState(sampleContent),
        nodeId: 'n008'
      },
      { type: 'NEXT' }
    )

    expect(atEnd.renderModel.end?.text).toBe('Episode end.')
    expect(atEnd.effects).toContainEqual({ type: 'STORY_END', nodeId: 'n008' })
  })
})
